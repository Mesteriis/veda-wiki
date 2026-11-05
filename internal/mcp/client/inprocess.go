package client

import (
	"errors"
	"net/http"
	"net/http/httptest"
)

// NewInProcess returns a read-only client that sends its requests straight to
// handler, in memory, with key as the bearer credential. It serves MCP to a
// remote agent: the REST handler of the same process applies the key's scope
// exactly as it does for any request. host and remote are copied from the
// agent's request so the handler's Host and address checks see the agent.
func NewInProcess(handler http.Handler, key, host, remote string) (*Client, error) {
	if handler == nil || !validKey(key) || host == "" {
		return nil, errors.New("invalid in-process configuration")
	}
	return &Client{
		base: "http://" + host, keyValue: key, readOnly: true, label: "mcp/remote",
		http: &http.Client{Transport: inProcess{handler: handler, remote: remote}},
	}, nil
}

// inProcess serves a request through a handler without a network.
type inProcess struct {
	handler http.Handler
	remote  string
}

func (t inProcess) RoundTrip(r *http.Request) (*http.Response, error) {
	request := r.Clone(r.Context())
	request.RequestURI = request.URL.RequestURI()
	request.RemoteAddr = t.remote
	recorder := httptest.NewRecorder()
	t.handler.ServeHTTP(recorder, request)
	response := recorder.Result()
	response.Request = r
	return response, nil
}
