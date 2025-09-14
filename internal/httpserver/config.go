// Licensed to Elasticsearch B.V. under one or more agreements.
// Elasticsearch B.V. licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

package httpserver

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	ucfg "github.com/elastic/go-ucfg"
	"github.com/elastic/go-ucfg/yaml"
	"go.uber.org/zap"
)

type config struct {
	AsSequence bool   `config:"as_sequence"`
	Rules      []rule `config:"rules"`
}

type rule struct {
	Path    string   `config:"path"`
	Methods []string `config:"methods"`

	User           string              `config:"user"`
	Password       string              `config:"password"`
	QueryParams    map[string][]string `config:"query_params"`
	RequestBody    string              `config:"request_body"`
	RequestHeaders map[string][]string `config:"request_headers"`

	Responses []response `config:"responses"`
}

type response struct {
	Headers    map[string][]*tpl `config:"headers"`
	Body       *tpl              `config:"body"`
	CEL        *string           `config:"cel"`
	StatusCode int               `config:"status_code"`

	cel *program
}

type tpl struct {
	*template.Template
}

func (t *tpl) Unpack(in string) error {
	parsed, err := template.New("").
		Option("missingkey=zero").
		Funcs(template.FuncMap{
			"env":         env,
			"hostname":    hostname,
			"sum":         sum,
			"file":        file,
			"glob":        filepath.Glob,
			"minify_json": minify,
		}).
		Parse(in)
	if err != nil {
		return err
	}

	*t = tpl{Template: parsed}

	return nil
}

func newConfigFromFile(file string, log *zap.SugaredLogger) (*config, error) {
	if file == "" {
		return nil, errors.New("a rules config file is required")
	}

	cfg, err := yaml.NewConfigWithFile(file, ucfg.PathSep("."))
	if err != nil {
		return nil, err
	}

	var config config
	if err := cfg.Unpack(&config); err != nil {
		return nil, err
	}

	for i := range config.Rules {
		rule := &config.Rules[i]
		for j := range rule.Responses {
			resp := &rule.Responses[j]
			if resp.CEL == nil {
				continue
			}

			resp.cel, err = newProgram(*resp.CEL, log)
			if err != nil {
				return nil, err
			}
		}
	}

	return &config, nil
}

func env(key string) string {
	return os.Getenv(key)
}

func hostname() string {
	h, _ := os.Hostname()
	return h
}

func sum(a, b int) int {
	return a + b
}

func file(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func minify(body string) (string, error) {
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	err := enc.Encode(json.RawMessage(body))
	return strings.TrimSpace(buf.String()), err
}
