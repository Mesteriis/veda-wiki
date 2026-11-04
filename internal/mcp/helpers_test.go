package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Mesteriis/veda-wiki/internal/mcp/client"
)

const testKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// fakeREST records requests and answers them with a handler.
type fakeREST struct {
	mu       sync.Mutex
	requests []recorded
}

type recorded struct {
	Method, URI, Body string
}

func (f *fakeREST) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func (f *fakeREST) all() []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recorded(nil), f.requests...)
}

// routes maps "METHOD /path" to a JSON response body; unknown routes get 404.
func startREST(t *testing.T, routes map[string]string) (*fakeREST, string) {
	t.Helper()
	fake := &fakeREST{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		fake.mu.Lock()
		fake.requests = append(fake.requests, recorded{r.Method, r.URL.RequestURI(), string(body)})
		fake.mu.Unlock()
		response, ok := routes[r.Method+" "+r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not_found"}`))
			return
		}
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(server.Close)
	return fake, server.URL
}

func keyFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(path, []byte(testKey), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// connect starts NewServer over in-memory transports and returns a client session.
func connect(t *testing.T, base string, readOnly bool) *sdk.ClientSession {
	t.Helper()
	opts := client.Options{URL: base, KeyFile: keyFile(t)}
	if readOnly {
		opts = client.Options{URL: base, ReaderKeyFile: opts.KeyFile}
	}
	c, err := client.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	serverSession, err := NewServer(c).Connect(t.Context(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	session, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "0"}, nil).Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// call invokes a tool with raw JSON arguments.
func call(t *testing.T, session *sdk.ClientSession, name, arguments string) *sdk.CallToolResult {
	t.Helper()
	result, err := session.CallTool(t.Context(), &sdk.CallToolParams{Name: name, Arguments: json.RawMessage(arguments)})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return result
}

func text(t *testing.T, result *sdk.CallToolResult) string {
	t.Helper()
	if len(result.Content) != 1 {
		t.Fatalf("content: %d blocks", len(result.Content))
	}
	block, ok := result.Content[0].(*sdk.TextContent)
	if !ok {
		t.Fatalf("content: %T", result.Content[0])
	}
	return block.Text
}

// success returns the decoded text of a successful result and checks that
// StructuredContent carries the same value.
func success(t *testing.T, result *sdk.CallToolResult) map[string]any {
	t.Helper()
	body := text(t, result)
	if result.IsError {
		t.Fatalf("unexpected refusal: %s", body)
	}
	var fromText, fromStructured map[string]any
	if err := json.Unmarshal([]byte(body), &fromText); err != nil {
		t.Fatalf("text is not JSON: %v", err)
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &fromStructured); err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(fromText, fromStructured) {
		t.Fatalf("structured %s differs from text %s", raw, body)
	}
	return fromText
}

func refusal(t *testing.T, result *sdk.CallToolResult) string {
	t.Helper()
	body := text(t, result)
	if !result.IsError {
		t.Fatalf("expected a refusal, got %s", body)
	}
	return body
}

func jsonEqual(a, b any) bool {
	left, errA := json.Marshal(a)
	right, errB := json.Marshal(b)
	return errA == nil && errB == nil && string(left) == string(right)
}
