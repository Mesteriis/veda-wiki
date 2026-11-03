package client

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKeyFileMustBePrivateRegular(t *testing.T) {
	if _, err := readKeyFile(writeKey(t, 0o600, testKey)); err != nil {
		t.Fatalf("valid key: %v", err)
	}
	cases := map[string]string{
		"group-readable": writeKey(t, 0o640, testKey),
		"world-readable": writeKey(t, 0o644, testKey),
		"63 characters":  writeKey(t, 0o600, testKey[:63]),
		"65 characters":  writeKey(t, 0o600, testKey+"\n"),
		"uppercase":      writeKey(t, 0o600, strings.ToUpper(testKey)),
		"non-hex":        writeKey(t, 0o600, strings.Repeat("g", 64)),
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(writeKey(t, 0o600, testKey), link); err != nil {
		t.Fatal(err)
	}
	cases["symlink"] = link
	cases["directory"] = t.TempDir()
	cases["missing"] = filepath.Join(t.TempDir(), "none")
	for name, path := range cases {
		if _, err := readKeyFile(path); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}
