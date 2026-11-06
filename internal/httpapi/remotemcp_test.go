package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// bearer adds an Authorization header to every request of the SDK client.
type bearer struct {
	key  string
	base http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.key)
	return b.base.RoundTrip(r)
}

func remoteFixture(t *testing.T) (Wiki, string) {
	t.Helper()
	s := testStore(t)
	source(t, s, "granted", "private")
	source(t, s, "hidden", "private")
	for _, id := range []string{"granted", "hidden"} {
		if _, err := s.put(id, "guide.md", "# "+id, 0); err != nil {
			t.Fatal(err)
		}
	}
	key, err := s.auth().CreateAccessKey("agent", []string{"granted"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return Wiki{Store: s, Token: "test-owner", ReaderToken: "test-reader", Activity: &ClientActivity{}}, key
}

func connectRemote(t *testing.T, w Wiki, key string) *sdk.ClientSession {
	t.Helper()
	server := httptest.NewServer(w)
	t.Cleanup(server.Close)
	transport := &sdk.StreamableClientTransport{Endpoint: server.URL + "/mcp", HTTPClient: &http.Client{Transport: bearer{key, http.DefaultTransport}}, MaxRetries: -1}
	session, err := sdk.NewClient(&sdk.Implementation{Name: "remote-test", Version: "0"}, nil).Connect(t.Context(), transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestRemoteMCPServesReaderToolsForTheKeysSourcesOnly(t *testing.T) {
	w, key := remoteFixture(t)
	session := connectRemote(t, w, key)
	tools, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range tools.Tools {
		names[tool.Name] = true
	}
	if len(tools.Tools) != 15 || names["veda_write_article"] || !names["veda_read"] {
		t.Fatalf("remote tools: %v", names)
	}
	result, err := session.CallTool(t.Context(), &sdk.CallToolParams{Name: "veda_sources", Arguments: map[string]any{}})
	if err != nil || result.IsError {
		t.Fatalf("sources: %v %+v", err, result)
	}
	text, _ := json.Marshal(result.Content)
	if !strings.Contains(string(text), "granted") || strings.Contains(string(text), "hidden") {
		t.Fatalf("remote agent saw sources outside its key: %s", text)
	}
	write, err := session.CallTool(t.Context(), &sdk.CallToolParams{Name: "veda_write_article", Arguments: map[string]any{"source_id": "granted", "path": "x.md", "text": "# X", "expected_revision": 0}})
	if err != nil || write == nil || !write.IsError {
		t.Fatalf("a remote write was not refused: %v %+v", err, write)
	}
	if _, written := w.Store.state(t).Articles["granted/x.md"]; written {
		t.Fatal("a remote write reached the store")
	}
	seen := activityOf(t, w)
	if len(seen.Clients) != 1 || seen.Clients[0].Mode != "remote" {
		t.Fatalf("remote activity: %+v", seen.Clients)
	}
}

func TestRemoteMCPRefusesOtherCredentials(t *testing.T) {
	w, key := remoteFixture(t)
	server := httptest.NewServer(w)
	t.Cleanup(server.Close)
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`
	post := func(url, token string) *http.Response {
		t.Helper()
		r, _ := http.NewRequest("POST", url, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}
	for name, token := range map[string]string{"none": "", "owner": "test-owner", "reader token": "test-reader", "unknown": strings.Repeat("a", 64)} {
		resp := post(server.URL+"/mcp", token)
		if resp.StatusCode != 401 || resp.Header.Get("WWW-Authenticate") == "" {
			t.Fatalf("%s: %d", name, resp.StatusCode)
		}
	}
	if resp := post(server.URL+"/mcp", key); resp.StatusCode != 200 {
		t.Fatalf("valid key: %d", resp.StatusCode)
	}
	public := httptest.NewServer(Wiki{Store: w.Store, Public: true})
	t.Cleanup(public.Close)
	if resp := post(public.URL+"/mcp", key); resp.StatusCode != 404 {
		t.Fatalf("public listener served MCP: %d", resp.StatusCode)
	}
	if err := w.Store.auth().RevokeAccessKey("agent"); err != nil {
		t.Fatal(err)
	}
	if resp := post(server.URL+"/mcp", key); resp.StatusCode != 401 {
		t.Fatalf("revoked key: %d", resp.StatusCode)
	}
}

func TestRemoteMCPBehindAProxyAndAtItsEdges(t *testing.T) {
	w, key := remoteFixture(t)
	w.AllowedHosts = []string{"wiki.example", "127.0.0.1"}
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`
	call := func(method, host, token, payload string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://"+host+"/mcp", strings.NewReader(payload))
		r.RemoteAddr = "127.0.0.1:40000"
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		r.Header.Set("Authorization", "Bearer "+token)
		rw := httptest.NewRecorder()
		w.ServeHTTP(rw, r)
		return rw
	}
	if got := call("POST", "wiki.example", key, body); got.Code != 200 {
		t.Fatalf("a proxy on loopback with the public name was refused: %d %s", got.Code, got.Body.String())
	}
	if got := call("POST", "evil.example", key, body); got.Code != 403 {
		t.Fatalf("an unknown Host was served: %d", got.Code)
	}
	if got := call("GET", "wiki.example", key, ""); got.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET on stateless MCP: %d", got.Code)
	}
	if got := call("POST", "wiki.example", key, strings.Repeat(" ", remoteMCPBodyLimit+1)+body); got.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body: %d", got.Code)
	}
}

func TestRemoteMCPCannotReadAHiddenSource(t *testing.T) {
	w, key := remoteFixture(t)
	session := connectRemote(t, w, key)
	hidden, err := session.CallTool(t.Context(), &sdk.CallToolParams{Name: "veda_read", Arguments: map[string]any{"source_id": "hidden", "path": "guide.md"}})
	if err != nil || hidden == nil || !hidden.IsError {
		t.Fatalf("a hidden article was readable: %v %+v", err, hidden)
	}
	granted, err := session.CallTool(t.Context(), &sdk.CallToolParams{Name: "veda_read", Arguments: map[string]any{"source_id": "granted", "path": "guide.md"}})
	if err != nil || granted.IsError {
		t.Fatalf("the granted article was not readable: %v %+v", err, granted)
	}
}
