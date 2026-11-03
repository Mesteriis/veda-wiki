package client

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"strings"
)

// tlsConfig always requires TLS 1.2 or newer. When SSL_CERT_FILE or
// SSL_CERT_DIR (a colon-separated list of directories) is set, only the
// certificates they name are trusted: the pool starts empty, as with OpenSSL
// in the Python adapter, so the system roots do not apply. Go reads these
// variables itself only on Unix systems other than macOS. When neither is set,
// RootCAs stays nil and the system roots are used. Unreadable files and
// directories are skipped, as Go does.
func tlsConfig() *tls.Config {
	return &tls.Config{RootCAs: environmentRoots(), MinVersion: tls.VersionTLS12}
}

// environmentRoots returns nil when no certificate variable is set.
func environmentRoots() *x509.CertPool {
	file, dirs := os.Getenv("SSL_CERT_FILE"), os.Getenv("SSL_CERT_DIR")
	if file == "" && dirs == "" {
		return nil
	}
	roots := x509.NewCertPool()
	var paths []string
	if file != "" {
		paths = append(paths, file)
	}
	for _, dir := range strings.Split(dirs, ":") {
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			paths = append(paths, filepath.Join(dir, entry.Name()))
		}
	}
	for _, path := range paths {
		if data, err := os.ReadFile(path); err == nil {
			roots.AppendCertsFromPEM(data)
		}
	}
	return roots
}
