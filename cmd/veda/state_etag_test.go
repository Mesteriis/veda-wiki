package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStateETagIsScopedAndChangesWithContent(t *testing.T) {
	s := testStore(t)
	source(t, s, "public", "public")
	source(t, s, "private", "private")
	if _, err := s.put("private", "secret.md", "# Private fixture", 0); err != nil {
		t.Fatal(err)
	}
	w := Wiki{Store: s, Token: "test-owner"}
	call := func(auth bool, tag string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/api/state", nil)
		if auth {
			r.Header.Set("Authorization", "Bearer test-owner")
		}
		if tag != "" {
			r.Header.Set("If-None-Match", tag)
		}
		out := httptest.NewRecorder()
		w.ServeHTTP(out, r)
		return out
	}
	first := call(true, "")
	tag := first.Header().Get("ETag")
	if first.Code != 200 || tag == "" || !strings.Contains(first.Body.String(), "Private fixture") {
		t.Fatal("private state or ETag missing")
	}
	unchanged := call(true, tag)
	if unchanged.Code != http.StatusNotModified || unchanged.Body.Len() != 0 || unchanged.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("unchanged state was not safely suppressed")
	}
	guest := call(false, tag)
	if guest.Code != 200 || strings.Contains(guest.Body.String(), "Private fixture") || guest.Header().Get("ETag") == tag {
		t.Fatal("private validator crossed the guest boundary")
	}
	if _, err := s.put("private", "secret.md", "# Updated fixture", 1); err != nil {
		t.Fatal(err)
	}
	changed := call(true, tag)
	if changed.Code != 200 || changed.Header().Get("ETag") == tag || !strings.Contains(changed.Body.String(), "Updated fixture") {
		t.Fatal("changed state was not returned")
	}
}

func TestStateETagStableWithDuplicateSourceTitles(t *testing.T) {
	s := testStore(t)
	for _, id := range []string{"alpha", "beta"} {
		if err := s.addSource(Source{ID: id, Title: "Shared title", Kind: "wiki", Visibility: "public"}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.put(id, "guide.md", "# Fixture", 0); err != nil {
			t.Fatal(err)
		}
	}
	w := Wiki{Store: s, Public: true}
	first := request(w, "GET", "/api/state", "", false)
	for i := 0; i < 25; i++ {
		next := request(w, "GET", "/api/state", "", false)
		if next.Header().Get("ETag") != first.Header().Get("ETag") || next.Body.String() != first.Body.String() {
			t.Fatal("unchanged groups produced unstable state")
		}
	}
}

func TestStateETagDoesNotKeepRevokedReaderContent(t *testing.T) {
	s := testStore(t)
	source(t, s, "private", "private")
	if _, err := s.put("private", "secret.md", "# Private fixture", 0); err != nil {
		t.Fatal(err)
	}
	key, err := s.createAccessKey("agent", []string{"private"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	w := Wiki{Store: s, Token: "test-owner"}
	call := func(tag string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/api/state", nil)
		r.Header.Set("Authorization", "Bearer "+key)
		r.Header.Set("If-None-Match", tag)
		out := httptest.NewRecorder()
		w.ServeHTTP(out, r)
		return out
	}
	first := call("")
	if first.Code != 200 || !strings.Contains(first.Body.String(), "Private fixture") {
		t.Fatal("scoped reader could not read its source")
	}
	if err := s.revokeAccessKey("agent"); err != nil {
		t.Fatal(err)
	}
	after := call(first.Header().Get("ETag"))
	if after.Code != 200 || strings.Contains(after.Body.String(), "Private fixture") {
		t.Fatal("revoked reader retained private state")
	}
}
