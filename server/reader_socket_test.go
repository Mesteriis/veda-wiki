package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalReaderDoesNotGrantWritesOrAdministration(t *testing.T) {
	s := testStore(t)
	source(t, s, "private", "private")
	if _, e := s.put("private", "guide.md", "# private fixture", 0); e != nil {
		t.Fatal(e)
	}
	w := Wiki{Store: s, ReaderLocal: true}
	call := func(method, path, body string, public bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer arbitrary")
		rec := httptest.NewRecorder()
		w.Public = public
		w.ServeHTTP(rec, r)
		return rec
	}
	if r := call("GET", "/api/v1/articles?source_id=private&path=guide.md", "", false); r.Code != 200 {
		t.Fatal("local reader failed", r.Code)
	}
	if r := call("POST", "/api/v1/articles", `{"source_id":"private","path":"x.md","text":"# X","expected_revision":0}`, false); r.Code != 403 {
		t.Fatal("local reader wrote", r.Code)
	}
	if r := call("GET", "/api/v1/conflicts", "", false); r.Code != 404 {
		t.Fatal("local reader reached admin", r.Code)
	}
	if r := call("GET", "/api/v1/articles?source_id=private&path=guide.md", "", true); r.Code != 404 {
		t.Fatal("public accepted local reader", r.Code)
	}
}
func TestReaderSocketRequiresPrivateOwnedDirectoryAndNoExistingPath(t *testing.T) {
	parent, err := os.MkdirTemp("/tmp", "veda-socket-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(parent) })
	if e := os.Chmod(parent, 0700); e != nil {
		t.Fatal(e)
	}
	name := filepath.Join(parent, "reader.sock")
	l, e := listenReaderSocket(name)
	if e != nil {
		t.Fatal(e)
	}
	info, e := os.Lstat(name)
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatal("socket permissions", e)
	}
	if _, e := listenReaderSocket(name); e == nil {
		t.Fatal("existing path replaced")
	}
	l.Close()
	if e := os.Chmod(parent, 0755); e != nil {
		t.Fatal(e)
	}
	if _, e := listenReaderSocket(name); e == nil {
		t.Fatal("non-private parent accepted")
	}
}
