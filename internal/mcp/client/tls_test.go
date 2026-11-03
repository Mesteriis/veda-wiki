package client

import (
	"crypto/tls"
	"crypto/x509"
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

func writeCert(t *testing.T, path string, cert *x509.Certificate) {
	t.Helper()
	block := &pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestCertificateEnvironmentReplacesSystemRoots checks that a set variable
// yields a pool holding exactly the named certificates, and that an unset
// environment leaves RootCAs nil so the system roots apply.
func TestCertificateEnvironmentReplacesSystemRoots(t *testing.T) {
	first := httptest.NewTLSServer(http.NotFoundHandler())
	defer first.Close()
	second := httptest.NewUnstartedServer(http.NotFoundHandler())
	second.StartTLS()
	defer second.Close()
	dir := t.TempDir()
	fileCert, dirCert := filepath.Join(dir, "file.pem"), filepath.Join(dir, "certs", "dir.pem")
	if err := os.Mkdir(filepath.Dir(dirCert), 0o700); err != nil {
		t.Fatal(err)
	}
	writeCert(t, fileCert, first.Certificate())
	writeCert(t, dirCert, second.Certificate())

	t.Setenv("SSL_CERT_FILE", "")
	t.Setenv("SSL_CERT_DIR", "")
	config := tlsConfig()
	if config.RootCAs != nil || config.MinVersion != tls.VersionTLS12 {
		t.Fatalf("unset environment: RootCAs %v, MinVersion %x", config.RootCAs, config.MinVersion)
	}

	t.Setenv("SSL_CERT_FILE", fileCert)
	t.Setenv("SSL_CERT_DIR", filepath.Dir(dirCert))
	pool := tlsConfig().RootCAs
	want := x509.NewCertPool()
	want.AddCert(first.Certificate())
	want.AddCert(second.Certificate())
	if pool == nil || !pool.Equal(want) {
		t.Fatal("pool is not exactly the certificates named by the environment")
	}

	t.Setenv("SSL_CERT_DIR", "")
	t.Setenv("SSL_CERT_FILE", filepath.Join(dir, "missing.pem"))
	if pool := tlsConfig().RootCAs; pool == nil || !pool.Equal(x509.NewCertPool()) {
		t.Fatal("an unreadable SSL_CERT_FILE must yield an empty pool, not the system roots")
	}
}
