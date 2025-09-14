// Licensed to Elasticsearch B.V. under one or more agreements.
// Elasticsearch B.V. licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestProgramEval(t *testing.T) {
	testCases := []struct {
		name    string
		req     *http.Request
		cel     string
		want    string
		wantErr bool
	}{
		{
			name: "json_string",
			cel:  `{"a":1}.encode_json()`,
			want: `{"a":1}`,
		},
		{
			name:    "output must be string",
			cel:     `{"a":1}`,
			wantErr: true,
		},
		{
			name: "echo request",
			req:  httptest.NewRequest(http.MethodGet, "/a/1?b=2", strings.NewReader(`{"a":1}`)),
			cel:  `req.encode_json()`,
			want: `
{
  "Body": "eyJhIjoxfQ==",
  "Close": false,
  "ContentLength": 7,
  "Header": {},
  "Host": "example.com",
  "Method": "GET",
  "Proto": "HTTP/1.1",
  "ProtoMajor": 1,
  "ProtoMinor": 1,
  "RequestURI": "/a/1?b=2",
  "URL": {
    "ForceQuery": false,
    "Fragment": "",
    "Host": "",
    "Opaque": "",
    "Path": "/a/1",
    "Query": {
      "b": [
        "2"
      ]
    },
    "RawFragment": "",
    "RawPath": "",
    "RawQuery": "b=2",
    "Scheme": "",
    "User": null
  }
}`,
		},
		{
			name: "range over limit",
			req:  httptest.NewRequest(http.MethodGet, "/?limit=2", strings.NewReader(`{"a":1}`)),
			cel:  `range(int(req.URL.Query.limit[0])).map(i, {"id":i}).encode_json()`,
			want: `[{"id":0},{"id":1}]`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := newProgram(tc.cel, zap.NewNop().Sugar())
			if err != nil {
				t.Fatal(err)
			}

			r := tc.req
			if r == nil {
				r = httptest.NewRequest(http.MethodGet, "/", nil)
			}

			got, err := p.evalAsString(r)
			if err != nil {
				if tc.wantErr {
					t.Logf("Expected error: %v", err)
					return
				}
				t.Fatal(err)
			}
			if tc.wantErr {
				t.Errorf("got %v, want error", got)
			}

			if got != tc.want {
				assert.JSONEqf(t, tc.want, got, "got: %v\nwant: %v", got, tc.want)
			}
		})
	}
}
