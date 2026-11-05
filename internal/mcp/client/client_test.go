package client

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTimeoutsByRoute(t *testing.T) {
	calls := []struct {
		method, route string
		body          any
	}{
		{"POST", "/api/v1/github/connections", map[string]any{"id": "test", "adopt_existing": true}},
		{"POST", "/api/v1/github/connections", map[string]any{"id": "test"}},
		{"GET", "/api/v1/github/connections", nil},
		{"POST", "/api/v1/sources/reconcile", map[string]any{"source_id": "one", "destination_id": "two"}},
		{"POST", "/api/v1/sources/mirrors", map[string]any{"source_id": "one", "destination_id": "two"}},
	}
	want := []time.Duration{360 * time.Second, 15 * time.Second, 15 * time.Second, 120 * time.Second, 120 * time.Second}
	for i, call := range calls {
		if got := Timeout(call.method, call.route, call.body); got != want[i] {
			t.Errorf("call %d: got %v want %v", i, got, want[i])
		}
	}
	if got := Timeout("POST", "/api/v1/github/connections", struct {
		AdoptExisting bool `json:"adopt_existing"`
	}{true}); got != 360*time.Second {
		t.Errorf("struct body: %v", got)
	}
	if got := Timeout("GET", "/api/v1/sources/reconcile", nil); got != 15*time.Second {
		t.Errorf("GET reconcile: %v", got)
	}
}

func TestRejectsNonLoopbackAndCredentialsInURL(t *testing.T) {
	key := writeKey(t, 0o600, testKey)
	for _, u := range []string{
		"http://example.com", "http://u:p@127.0.0.1", "http://127.0.0.1/path", "http://127.0.0.1?x=1",
		"http://127.0.0.1#f", "http://127.0.0.1:99999", "ftp://127.0.0.1", "",
	} {
		if _, err := New(Options{URL: u, KeyFile: key}); err == nil {
			t.Errorf("%q: want error", u)
		}
	}
	for _, u := range []string{"http://127.0.0.1:8080", "http://localhost", "http://[::1]:8080/", "http://LOCALHOST:1"} {
		if _, err := New(Options{URL: u, KeyFile: key}); err != nil {
			t.Errorf("%q: %v", u, err)
		}
	}
}

func TestRemoteHTTPSOnlyForReaderKeyFile(t *testing.T) {
	key := writeKey(t, 0o600, testKey)
	ok := Options{URL: "https://veda.example", ReaderKeyFile: key, AllowRemoteReaderHTTPS: true}
	c, err := New(ok)
	if err != nil || !c.ReadOnly() {
		t.Fatalf("reader https: %v", err)
	}
	bad := map[string]Options{
		"owner key":      {URL: "https://veda.example", KeyFile: key, AllowRemoteReaderHTTPS: true},
		"not enabled":    {URL: "https://veda.example", ReaderKeyFile: key},
		"plain http":     {URL: "http://veda.example", ReaderKeyFile: key, AllowRemoteReaderHTTPS: true},
		"path":           {URL: "https://veda.example/path", ReaderKeyFile: key, AllowRemoteReaderHTTPS: true},
		"user":           {URL: "https://u@veda.example", ReaderKeyFile: key, AllowRemoteReaderHTTPS: true},
		"port range":     {URL: "https://veda.example:99999", ReaderKeyFile: key, AllowRemoteReaderHTTPS: true},
		"socket":         {URL: "https://veda.example", ReaderKeyFile: key, SocketPath: "/tmp/s", AllowRemoteReaderHTTPS: true},
		"both key files": {URL: "http://127.0.0.1", KeyFile: key, ReaderKeyFile: key},
	}
	for name, opts := range bad {
		if _, err := New(opts); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	if c, _ := New(Options{URL: "http://127.0.0.1", KeyFile: key}); c.ReadOnly() {
		t.Error("owner client must not be read-only")
	}
}

func newTestClient(t *testing.T, h http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	c, err := New(Options{URL: server.URL, KeyFile: writeKey(t, 0o600, testKey)})
	if err != nil {
		t.Fatal(err)
	}
	return c, server
}

func TestDoSendsAuthorizationAndBody(t *testing.T) {
	var gotAuth, gotType, gotBody, gotURI string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotType, gotURI = r.Header.Get("Authorization"), r.Header.Get("Content-Type"), r.URL.RequestURI()
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	got, err := c.Do(t.Context(), "POST", "/api/v1/sources?x=1", map[string]any{"id": "a<b"})
	if err != nil || string(got) != `{"ok":true}` {
		t.Fatalf("%s %v", got, err)
	}
	if gotAuth != "Bearer "+testKey || gotType != "application/json" || gotURI != "/api/v1/sources?x=1" || gotBody != `{"id":"a<b"}` {
		t.Fatalf("auth=%q type=%q uri=%q body=%q", gotAuth, gotType, gotURI, gotBody)
	}
	var decoded map[string]bool
	if json.Unmarshal(got, &decoded) != nil {
		t.Fatal("not JSON")
	}
}

func TestDoesNotFollowRedirect(t *testing.T) {
	hits := 0
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/target", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	})
	_, err := c.Do(t.Context(), "GET", "/redirect", nil)
	refused, ok := err.(Refused)
	if !ok || refused.Status != 302 || hits != 1 {
		t.Fatalf("err=%v hits=%d", err, hits)
	}
}

func TestDoesNotUseProxy(t *testing.T) {
	// Loopback targets bypass environment proxies anyway, so check the
	// transport itself: a nil Proxy never consults HTTP_PROXY or HTTPS_PROXY.
	for _, opts := range []Options{
		{URL: "http://127.0.0.1:8080", KeyFile: writeKey(t, 0o600, testKey)},
		{URL: "https://veda.example", ReaderKeyFile: writeKey(t, 0o600, testKey), AllowRemoteReaderHTTPS: true},
	} {
		c, err := New(opts)
		if err != nil {
			t.Fatal(err)
		}
		transport, ok := c.http.Transport.(*http.Transport)
		if !ok || transport.Proxy != nil {
			t.Fatalf("%s: transport %T uses a proxy function", opts.URL, c.http.Transport)
		}
	}
}

func TestResponseLimit(t *testing.T) {
	size := 24<<20 + 1
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`"` + strings.Repeat("a", size-2) + `"`))
	})
	if _, err := c.Do(t.Context(), "GET", "/x", nil); err == nil || !strings.Contains(err.Error(), "exceeds MCP limit") {
		t.Fatalf("got %v", err)
	}
	c, _ = newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`"` + strings.Repeat("a", MaxResponseBytes-2) + `"`))
	})
	if got, err := c.Do(t.Context(), "GET", "/x", nil); err != nil || len(got) != MaxResponseBytes {
		t.Fatalf("exact limit: %d %v", len(got), err)
	}
}

func TestInvalidJSONResponse(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`nope`)) })
	if _, err := c.Do(t.Context(), "GET", "/x", nil); err == nil {
		t.Fatal("want error")
	}
}

func TestBadKeyFileFailsRequest(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) })
	c.keyFile = writeKey(t, 0o644, testKey)
	if _, err := c.Do(t.Context(), "GET", "/x", nil); err == nil {
		t.Fatal("want error")
	}
}

func TestDoLabelsTheMCPMode(t *testing.T) {
	var got []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("X-Veda-Client"))
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)
	key := writeKey(t, 0o600, testKey)
	for _, opts := range []Options{{KeyFile: key}, {KeyFile: key, Editor: true}, {ReaderKeyFile: key}} {
		opts.URL = server.URL
		c, err := New(opts)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = c.Do(t.Context(), "GET", "/api/v1/sources", nil); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Join(got, ",") != "mcp/owner,mcp/editor,mcp/reader" {
		t.Fatalf("labels %v", got)
	}
}
