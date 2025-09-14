// Licensed to Elasticsearch B.V. under one or more agreements.
// Elasticsearch B.V. licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

package httpserver

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/elastic/stream/internal/log"
	"github.com/elastic/stream/internal/output"
)

func TestHTTPServer(t *testing.T) {
	cfg := `---
    rules:
    - path: "/path1/test"
      methods: ["GET"]

      user: username
      password: passwd
      query_params:
        p1: ["v1"]
        p2: null
      request_headers:
        accept: ["application/json"]

      responses:
      - headers:
          x-foo: ["test"]
        status_code: 200
        body: |-
          {"next": "http://{{ hostname }}/page/{{ sum (.req_num) 1 }}"}

    - path: "/page/{pagenum:[0-9]}"
      methods: ["POST"]

      responses:
      - status_code: 200
        body: "{{ .request.vars.pagenum }}"
        headers:
          content-type: ["text/plain"]

    - path: "/static/minify"
      methods: ["GET"]

      responses:
      - status_code: 200
        body: |-
          {{ minify_json ` + "`" + `
          {
          	"key1": "value1",
          	"key2": "<value2>"
          }
          ` + "`" + `}}

    - path: "/cel"
      methods: ["GET"]
      responses:
      - status_code: 200
        cel: '{"path": req.RequestURI}.encode_json()'
`

	f, err := ioutil.TempFile("", "test")
	require.NoError(t, err)

	t.Cleanup(func() { os.Remove(f.Name()) })

	_, err = f.WriteString(cfg)
	require.NoError(t, err)

	opts := Options{
		Options: &output.Options{
			Addr: "localhost:0",
		},
		ConfigPath: f.Name(),
	}

	logger, err := log.NewLogger()
	require.NoError(t, err)

	_, addr := startTestServer(t, &opts, logger.Sugar())

	t.Run("request does not match path unless all requirements are met", func(t *testing.T) {
		req, err := http.NewRequest("GET", "http://"+addr+"/path1/test?p1=v1", nil)
		require.NoError(t, err)
		req.Header.Add("accept", "application/json")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		resp.Body.Close()

		assert.Equal(t, 404, resp.StatusCode) // must fail because user/pass is missing

		req.SetBasicAuth("username", "passwd")

		resp, err = http.DefaultClient.Do(req)
		require.NoError(t, err)

		assert.Equal(t, 200, resp.StatusCode) // should work when all criteria matches

		body, err := ioutil.ReadAll(resp.Body)
		require.NoError(t, err)
		resp.Body.Close()

		hostname, err := os.Hostname()
		require.NoError(t, err)

		assert.JSONEq(t, fmt.Sprintf(`{"next": "http://%s/page/2"}`, hostname), string(body))
		assert.Equal(t, "test", resp.Header.Get("x-foo"))
	})

	t.Run("can map request info in response templates", func(t *testing.T) {
		req, err := http.NewRequest("POST", "http://"+addr+"/page/2", nil)
		require.NoError(t, err)

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)

		assert.Equal(t, 200, resp.StatusCode)

		body, err := ioutil.ReadAll(resp.Body)
		require.NoError(t, err)
		resp.Body.Close()

		assert.JSONEq(t, "2", string(body))
		assert.Equal(t, "text/plain", resp.Header.Get("content-type"))
	})

	t.Run("request has rejected parameter", func(t *testing.T) {
		req, err := http.NewRequest("GET", "http://"+addr+"/path1/test?p1=v1&p2=bad", nil)
		require.NoError(t, err)

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		resp.Body.Close()

		assert.Equal(t, 404, resp.StatusCode) // must fail because p2 is present

		body, err := ioutil.ReadAll(resp.Body)
		require.NoError(t, err)
		resp.Body.Close()

		assert.Equal(t, []byte{}, body)
	})

	t.Run("minify static JSON", func(t *testing.T) {
		req, err := http.NewRequest("GET", "http://"+addr+"/static/minify", nil)
		require.NoError(t, err)

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)

		body, err := ioutil.ReadAll(resp.Body)
		require.NoError(t, err)
		resp.Body.Close()

		assert.Equal(t, `{"key1":"value1","key2":"<value2>"}`, string(body))
	})

	t.Run("execute_cel_program", func(t *testing.T) {
		req, err := http.NewRequest("GET", "http://"+addr+"/cel", nil)
		require.NoError(t, err)

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		resp.Body.Close()

		assert.Equal(t, `{"path":"/cel"}`, string(body))
	})
}

func TestRunAsSequence(t *testing.T) {
	cfg := `---
  as_sequence: true
  rules:
  - path: "/path/1"
    methods: ["GET"]
    request_headers:
      accept: ["application/json"]
    responses:
    - status_code: 200
      body: |-
        {"req1": "{{ .req_num }}"}
    - status_code: 200
      body: |-
        {"req2": "{{ .req_num }}"}
  - path: "/path/2"
    methods: ["GET"]
    request_headers:
      accept: ["application/json"]
    responses:
    - status_code: 200
      body: |-
        {"req3": "{{ .req_num }}"}
  - path: "/path/3"
    methods: ["GET"]
    request_headers:
      accept: ["application/json"]
    responses:
    - status_code: 200
      body: |-
        {"req4": "{{ .req_num }}"}
    - status_code: 200
      body: |-
        {"req5": "{{ .req_num }}"}
  - path: "/path/4"
    methods: ["GET"]
    request_headers:
      accept: ["application/json"]
    responses:
    - status_code: 200
      body: |-
        {"req6": "{{ .req_num }}"}
  - path: "/path/5"
    methods: ["GET"]
    request_headers:
      accept: ["application/json"]
    responses:
    - status_code: 200
      body: |-
        {"req7": "{{ .req_num }}"}
`

	f, err := ioutil.TempFile("", "test")
	require.NoError(t, err)

	t.Cleanup(func() { os.Remove(f.Name()) })

	_, err = f.WriteString(cfg)
	require.NoError(t, err)

	opts := Options{
		Options: &output.Options{
			Addr: "localhost:0",
		},
		ConfigPath: f.Name(),
	}

	logger, err := log.NewLogger()
	logger = logger.WithOptions(zap.OnFatal(zapcore.WriteThenPanic))
	require.NoError(t, err)

	t.Run("requests succeed if made in the expected order", func(t *testing.T) {
		server, addr := startTestServer(t, &opts, logger.Sugar())

		reqTests := []struct {
			path         string
			expectedBody string
		}{
			{"http://" + addr + "/path/1", `{"req1": "1"}`},
			{"http://" + addr + "/path/1", `{"req2": "2"}`},
			{"http://" + addr + "/path/2", `{"req3": "1"}`},
			{"http://" + addr + "/path/3", `{"req4": "1"}`},
			{"http://" + addr + "/path/3", `{"req5": "2"}`},
			{"http://" + addr + "/path/4", `{"req6": "1"}`},
			{"http://" + addr + "/path/5", `{"req7": "1"}`},
		}

		buf := new(bytes.Buffer)
		server.server.Handler = http.HandlerFunc(inspectPanic(server.server.Handler, buf))

		for _, reqTest := range reqTests {
			req, err := http.NewRequest("GET", reqTest.path, nil)
			require.NoError(t, err)
			req.Header.Add("accept", "application/json")

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			assert.Equal(t, 200, resp.StatusCode)

			body, err := ioutil.ReadAll(resp.Body)
			require.NoError(t, err)
			resp.Body.Close()

			assert.JSONEq(t, reqTest.expectedBody, string(body))
			assert.Equal(t, "", buf.String())
		}
	})

	t.Run("requests fail if made in the wrong order", func(t *testing.T) {
		server, addr := startTestServer(t, &opts, logger.Sugar())
		buf := new(bytes.Buffer)
		server.server.Handler = http.HandlerFunc(inspectPanic(server.server.Handler, buf))

		req, err := http.NewRequest("GET", "http://"+addr+"/path/1", nil)
		require.NoError(t, err)
		req.Header.Add("accept", "application/json")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		assert.Equal(t, 200, resp.StatusCode)

		body, err := ioutil.ReadAll(resp.Body)
		require.NoError(t, err)
		resp.Body.Close()

		assert.JSONEq(t, `{"req1": "1"}`, string(body))

		req, err = http.NewRequest("GET", "http://"+addr+"/path/2", nil)
		require.NoError(t, err)
		req.Header.Add("accept", "application/json")

		resp, err = http.DefaultClient.Do(req)
		require.NoError(t, err)
		assert.Equal(t, 500, resp.StatusCode)

		assert.Equal(t, "expecting to match request #1 in sequence, matched rule #2 instead, exiting", buf.String())
	})
}

func TestExitOnUnmatchedRule(t *testing.T) {
	cfg := `---
  rules:
  - path: "/path/1"
    methods: ["GET"]
    request_headers:
      accept: ["application/json"]
    responses:
    - status_code: 200
      body: |-
        {"req1": "{{ .req_num }}"}
`

	f, err := ioutil.TempFile("", "test")
	require.NoError(t, err)

	t.Cleanup(func() { os.Remove(f.Name()) })

	_, err = f.WriteString(cfg)
	require.NoError(t, err)

	opts := Options{
		Options: &output.Options{
			Addr: "localhost:0",
		},
		ConfigPath:          f.Name(),
		ExitOnUnmatchedRule: true,
	}

	logger, err := log.NewLogger()
	logger = logger.WithOptions(zap.OnFatal(zapcore.WriteThenPanic))
	require.NoError(t, err)

	server, addr := startTestServer(t, &opts, logger.Sugar())
	buf := new(bytes.Buffer)
	server.server.Handler = http.HandlerFunc(inspectPanic(server.server.Handler, buf))

	req, err := http.NewRequest("GET", "http://"+addr+"/path/2", nil)
	require.NoError(t, err)
	req.Header.Add("accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, 404, resp.StatusCode)

	assert.Equal(t, "--exit-on-unmatched-rule is set, exiting", buf.String())
}

func inspectPanic(h http.Handler, writer io.Writer) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				fmt.Fprintf(writer, "%v", err)
				w.WriteHeader(http.StatusInternalServerError)
			}
		}()
		h.ServeHTTP(w, r)
	}
}

func startTestServer(t *testing.T, opts *Options, logger *zap.SugaredLogger) (*Server, string) {
	server, err := New(opts, logger)
	require.NoError(t, err)

	t.Cleanup(func() { server.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	require.NoError(t, server.Start(ctx))

	addr := server.listener.Addr().(*net.TCPAddr).String()

	return server, addr
}
