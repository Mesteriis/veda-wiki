package httpapi

import (
	"net/http/httptest"
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
