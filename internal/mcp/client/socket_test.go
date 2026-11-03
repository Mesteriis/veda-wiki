package client

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func serveSocket(t *testing.T, dirMode os.FileMode) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "v")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.Chmod(dir, dirMode); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "s")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"auth":"` + r.Header.Get("Authorization") + `"}`))
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	_ = os.Chmod(path, 0o600)
	return path
}

func TestSocketMustBePrivate(t *testing.T) {
	open := serveSocket(t, 0o755)
	c, err := New(Options{URL: "http://127.0.0.1:8080", SocketPath: open})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Do(t.Context(), "GET", "/api/v1/sources", nil); err == nil {
		t.Fatal("0755 directory must be refused")
	}
	loose := serveSocket(t, 0o700)
	if err := os.Chmod(loose, 0o660); err != nil {
		t.Fatal(err)
	}
	c, _ = New(Options{URL: "http://127.0.0.1:8080", SocketPath: loose})
	if _, err := c.Do(t.Context(), "GET", "/api/v1/sources", nil); err == nil {
		t.Fatal("group-writable socket must be refused")
	}
}

func TestSocketRequestWithReaderKey(t *testing.T) {
	path := serveSocket(t, 0o700)
	c, err := New(Options{URL: "http://127.0.0.1:8080", SocketPath: path, KeyValue: testKey})
	if err != nil {
		t.Fatal(err)
	}
	if !c.ReadOnly() {
		t.Fatal("socket client must be read-only")
	}
	got, err := c.Do(t.Context(), "GET", "/x", nil)
	if err != nil || string(got) != `{"auth":"Bearer `+testKey+`"}` {
		t.Fatalf("got %s, %v", got, err)
	}
	c, _ = New(Options{URL: "http://127.0.0.1:8080", SocketPath: path})
	got, err = c.Do(t.Context(), "GET", "/x", nil)
	if err != nil || string(got) != `{"auth":""}` {
		t.Fatalf("keyless: got %s, %v", got, err)
	}
}

func TestSocketConfiguration(t *testing.T) {
	key := writeKey(t, 0o600, testKey)
	for name, opts := range map[string]Options{
		"relative":           {URL: "http://127.0.0.1", SocketPath: "s"},
		"owner key":          {URL: "http://127.0.0.1", SocketPath: "/tmp/s", KeyFile: key},
		"value and file":     {URL: "http://127.0.0.1", SocketPath: "/tmp/s", ReaderKeyFile: key, KeyValue: testKey},
		"value without sock": {URL: "http://127.0.0.1", ReaderKeyFile: key, KeyValue: testKey},
		"short value":        {URL: "http://127.0.0.1", SocketPath: "/tmp/s", KeyValue: testKey[:63]},
		"no credential":      {URL: "http://127.0.0.1"},
	} {
		if _, err := New(opts); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	if _, err := New(Options{URL: "http://127.0.0.1", SocketPath: "/tmp/s", ReaderKeyFile: key}); err != nil {
		t.Fatalf("reader key with socket: %v", err)
	}
}
