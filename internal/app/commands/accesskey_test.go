package commands

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

	"github.com/Mesteriis/veda-wiki/internal/auth"
	"github.com/Mesteriis/veda-wiki/internal/httpapi"
	"github.com/Mesteriis/veda-wiki/internal/importer"
	"github.com/Mesteriis/veda-wiki/internal/store/storetest"
)

func TestAccessKeyCommandCreatesPrivateFileAndRevokes(t *testing.T) {
	st := storetest.New(t)
	storetest.Source(t, st, "alpha", "private")
	keys := auth.Service{Store: st}
	token := strings.Repeat("a", 64)
	server := httptest.NewServer(httpapi.New(httpapi.Config{OwnerToken: token}, httpapi.Deps{Store: st, Observations: &importer.Observations{}}))
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
	if err := AccessKey(args, &output); err != nil {
		t.Fatal(err)
	}
	key, err := os.ReadFile(keyPath)
	if err != nil || len(key) != 64 || strings.Contains(output.String(), string(key)) {
		t.Fatal("key not saved privately or leaked to output")
	}
	if info, err := os.Stat(keyPath); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("incorrect key file permissions")
	}
	if got := keys.AccessScope(string(key), time.Now()); len(got) != 1 || got[0] != "alpha" {
		t.Fatal("wrong scope")
	}
	if err := AccessKey(args, &output); err == nil {
		t.Fatal("existing key file overwritten")
	}
	after, err := os.ReadFile(keyPath)
	if err != nil || !bytes.Equal(key, after) {
		t.Fatal("existing key file changed")
	}
	output.Reset()
	if err := AccessKey(append([]string{"list"}, common...), &output); err != nil {
		t.Fatal(err)
	}
	var metadata []auth.AccessKeyView
	if json.Unmarshal(output.Bytes(), &metadata) != nil || len(metadata) != 1 || strings.Contains(output.String(), string(key)) {
		t.Fatal("invalid or sensitive list")
	}
	output.Reset()
	if err := AccessKey(append([]string{"revoke", "--id", "agent"}, common...), &output); err != nil {
		t.Fatal(err)
	}
	if keys.AccessScope(string(key), time.Now()) != nil {
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
	if err := AccessKey(args, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "uncertain") {
		t.Fatal("uncertain result was hidden")
	}
	if info, err := os.Stat(keyPath); err != nil || info.Size() != 0 || info.Mode().Perm() != 0600 {
		t.Fatal("reserved file not preserved")
	}
}
