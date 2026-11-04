package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGeneratedReaderKeyPersistsAndIsPrivate(t *testing.T) {
	dir := t.TempDir()
	first, err := loadOrCreateKey(dir, "reader.key")
	if err != nil {
		t.Fatal(err)
	}
	second, err := loadOrCreateKey(dir, "reader.key")
	if err != nil || string(first) != string(second) {
		t.Fatal("reader key changed", err)
	}
	info, err := os.Stat(filepath.Join(dir, "reader.key"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("reader key permissions", err)
	}
	if err := os.Chmod(filepath.Join(dir, "reader.key"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadOrCreateKey(dir, "reader.key"); err == nil {
		t.Fatal("insecure reader key accepted")
	}
}
