package client

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"strings"
)

// tlsConfig adds the certificates named by SSL_CERT_FILE and SSL_CERT_DIR (a
// colon-separated list of directories) to the system roots, as OpenSSL did
// for the Python adapter. Go reads these variables itself only on Unix
// systems other than macOS. Without them it returns nil: system roots only.
// Unreadable files are skipped, as Go does.
func tlsConfig() *tls.Config {
	file, dirs := os.Getenv("SSL_CERT_FILE"), os.Getenv("SSL_CERT_DIR")
	if file == "" && dirs == "" {
		return nil
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		roots = x509.NewCertPool()
	}
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
	return &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
}
