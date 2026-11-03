package client

import (
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestHonorsCertificateEnvironment checks that SSL_CERT_FILE and SSL_CERT_DIR
// add trusted roots on every platform, as they did for the Python adapter's
// OpenSSL; Go itself ignores them on macOS.
func TestHonorsCertificateEnvironment(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	dir := t.TempDir()
	certFile := filepath.Join(dir, "ca.pem")
	block := &pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}
	if err := os.WriteFile(certFile, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	key := writeKey(t, 0o600, testKey)
	request := func() error {
		c, err := New(Options{URL: server.URL, ReaderKeyFile: key, AllowRemoteReaderHTTPS: true})
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.Do(t.Context(), http.MethodGet, "/x", nil)
		return err
	}
	t.Setenv("SSL_CERT_FILE", "")
	t.Setenv("SSL_CERT_DIR", "")
	if request() == nil {
		t.Fatal("untrusted certificate accepted")
	}
	t.Setenv("SSL_CERT_FILE", certFile)
	if err := request(); err != nil {
		t.Fatalf("SSL_CERT_FILE: %v", err)
	}
	t.Setenv("SSL_CERT_FILE", "")
	t.Setenv("SSL_CERT_DIR", filepath.Join(dir, "missing")+":"+dir)
	if err := request(); err != nil {
		t.Fatalf("SSL_CERT_DIR: %v", err)
	}
}
