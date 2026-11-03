package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAccessKeyCommandCreatesPrivateFileAndRevokes(t *testing.T) {
	s := testStore(t)
	source(t, s, "alpha", "private")
	token := strings.Repeat("a", 64)
	server := httptest.NewServer(Wiki{Store: s, Token: token})
	defer server.Close()
	dir := t.TempDir()
	owner, keyPath := filepath.Join(dir, "owner"), filepath.Join(dir, "agent")
	if err := os.WriteFile(owner, []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
	common := []string{"--url", server.URL, "--key-file", owner}
	expiry := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	args := append([]string{"create", "--id", "agent", "--sources", "alpha", "--expires-at", expiry, "--out", keyPath}, common...)
	var output bytes.Buffer
	if err := accessKeyCommand(args, &output); err != nil {
		t.Fatal(err)
	}
	key, err := os.ReadFile(keyPath)
	if err != nil || len(key) != 64 || strings.Contains(output.String(), string(key)) {
		t.Fatal("key not saved privately or leaked to output")
	}
	if info, err := os.Stat(keyPath); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("incorrect key file permissions")
	}
	if got := s.accessScope(string(key), time.Now()); len(got) != 1 || got[0] != "alpha" {
		t.Fatal("wrong scope")
	}
	if err := accessKeyCommand(args, &output); err == nil {
		t.Fatal("existing key file overwritten")
	}
	after, err := os.ReadFile(keyPath)
	if err != nil || !bytes.Equal(key, after) {
		t.Fatal("existing key file changed")
	}
	output.Reset()
	if err := accessKeyCommand(append([]string{"list"}, common...), &output); err != nil {
		t.Fatal(err)
	}
	var metadata []AccessKeyView
	if json.Unmarshal(output.Bytes(), &metadata) != nil || len(metadata) != 1 || strings.Contains(output.String(), string(key)) {
		t.Fatal("invalid or sensitive list")
	}
	output.Reset()
	if err := accessKeyCommand(append([]string{"revoke", "--id", "agent"}, common...), &output); err != nil {
		t.Fatal(err)
	}
	if s.accessScope(string(key), time.Now()) != nil {
		t.Fatal("key not revoked")
	}
}

func TestAccessKeyCommandReservesFileOnUncertainResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer srv.Close()
	dir := t.TempDir()
	owner, keyPath := filepath.Join(dir, "owner"), filepath.Join(dir, "agent")
	if err := os.WriteFile(owner, []byte(strings.Repeat("a", 64)), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"create", "--id", "agent", "--sources", "alpha", "--expires-at", time.Now().Add(time.Hour).UTC().Format(time.RFC3339), "--out", keyPath, "--url", srv.URL, "--key-file", owner}
	if err := accessKeyCommand(args, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "uncertain") {
		t.Fatal("uncertain result was hidden")
	}
	if info, err := os.Stat(keyPath); err != nil || info.Size() != 0 || info.Mode().Perm() != 0600 {
		t.Fatal("reserved file not preserved")
	}
}
