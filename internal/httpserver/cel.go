// Licensed to Elasticsearch B.V. under one or more agreements.
// Elasticsearch B.V. licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

package httpserver

import (
	"fmt"
	"io"
	"net/http"
	"reflect"

	"github.com/elastic/mito/lib"
	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/decls"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/ext"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/structpb"
)

type program struct {
	prg cel.Program
	ast *cel.Ast
}

func newProgram(src string, log *zap.SugaredLogger) (*program, error) {
	if src == "" {
		return nil, nil
	}

	env, err := cel.NewEnv(
		cel.VariableDecls(decls.NewVariable("req", types.DynType)),
		lib.Collections(),
		lib.Crypto(),
		lib.JSON(nil),
		lib.Time(),
		lib.Try(),
		lib.Debug(debug(log)),
		lib.File(nil),
		lib.MIME(nil),
		lib.Strings(),
		lib.Printf(),
		cel.OptionalTypes(cel.OptionalTypesVersion(lib.OptionalTypesVersion)),
		ext.TwoVarComprehensions(ext.TwoVarComprehensionsVersion(lib.OptionalTypesVersion)),
		cel.Function("range",
			cel.Overload(
				"range_int",
				[]*cel.Type{cel.IntType},
				cel.ListType(cel.IntType),
				cel.UnaryBinding(rangeFunc),
			),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create env: %w", err)
	}

	ast, iss := env.Compile(src)
	if iss.Err() != nil {
		return nil, fmt.Errorf("failed compilation: %w", iss.Err())
	}

	prg, err := env.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("failed program instantiation: %w", err)
	}
	return &program{prg: prg, ast: ast}, nil
}

func debug(log *zap.SugaredLogger) func(string, any) {
	log = log.Named("cel_debug")
	return func(tag string, value any) {
		level := "DEBUG"
		if _, ok := value.(error); ok {
			level = "ERROR"
		}
		log.Debugw(level, "tag", tag, "value", value)
	}
}

// range returns a list of integers from 0 to n-1 where n is the integer
// parameter:
//
//	range(<int>) -> <list<int>>
//
// Examples:
//
//	range(3)  // return [0, 1, 2]
//	range(0)  // return []
//	range(1)  // return [0]
func rangeFunc(arg ref.Val) ref.Val {
	n, ok := arg.(types.Int)
	if !ok {
		return types.ValOrErr(arg, "no such overload")
	}
	if n < 0 {
		return types.NewErr("range: argument must be non-negative")
	}

	result := make([]int64, n)
	for i := int64(0); i < int64(n); i++ {
		result[i] = i
	}
	return types.NewDynamicList(types.DefaultTypeAdapter, result)
}

func (p *program) eval(r *http.Request) (any, error) {
	rm, err := reqToMap(r)
	if err != nil {
		return nil, err
	}

	out, _, err := p.prg.Eval(map[string]interface{}{"req": rm})
	if err != nil {
		err = lib.DecoratedError{AST: p.ast, Err: err}
		return nil, fmt.Errorf("failed eval: %w", err)
	}

	v, err := out.ConvertToNative(reflect.TypeOf((*structpb.Value)(nil)))
	if err != nil {
		return nil, fmt.Errorf("failed proto conversion: %w", err)
	}

	switch v := v.(type) {
	case *structpb.Value:
		return v.AsInterface(), nil
	default:
		// This should never happen.
		return nil, fmt.Errorf("unexpected native conversion type: %T", v)
	}
}

func (p *program) evalAsString(r *http.Request) (string, error) {
	v, err := p.eval(r)
	if err != nil {
		return "", err
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("unexpected cel program return type: %T, wanted string", v)
	}
	return s, nil
}

func reqToMap(req *http.Request) (map[string]interface{}, error) {
	rm := map[string]interface{}{
		"Method":        req.Method,
		"Proto":         req.Proto,
		"ProtoMajor":    req.ProtoMajor,
		"ProtoMinor":    req.ProtoMinor,
		"Header":        req.Header,
		"ContentLength": req.ContentLength,
		"Close":         req.Close,
		"Host":          req.Host,
	}
	if req.URL != nil {
		u := req.URL

		var user map[string]any
		if u.User != nil {
			password, passwordSet := u.User.Password()
			user = map[string]interface{}{
				"Username":    u.User.Username(),
				"Password":    password,
				"PasswordSet": passwordSet,
			}
		}

		rm["URL"] = map[string]any{
			"ForceQuery":  u.ForceQuery,
			"Fragment":    u.Fragment,
			"Host":        u.Host,
			"Opaque":      u.Opaque,
			"Path":        u.Path,
			"RawFragment": u.RawFragment,
			"RawPath":     u.RawPath,
			"RawQuery":    u.RawQuery,
			"Query":       u.Query(),
			"Scheme":      u.Scheme,
			"User":        user,
		}
	}
	if req.RequestURI != "" {
		rm["RequestURI"] = req.RequestURI
	}
	if req.Body != nil {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		rm["Body"] = body
	}
	if req.TransferEncoding != nil {
		rm["TransferEncoding"] = req.TransferEncoding
	}
	if req.Trailer != nil {
		rm["Trailer"] = req.Trailer
	}
	return rm, nil
}
