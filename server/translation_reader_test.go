package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTranslationQueueRespectsReaderScope(t *testing.T) {
	s := testStore(t)
	for _, id := range []string{"alpha", "beta"} {
		if err := s.addSource(Source{ID: id, Title: id, Kind: "program", Visibility: "private"}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.put(id, "docs/setup.md", "# Setup", 0); err != nil {
			t.Fatal(err)
		}
	}
	key, err := s.createAccessKey("agent", []string{"alpha"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	w := Wiki{Store: s, Token: "owner", ReaderToken: "reader"}
	call := func(token string, public bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/api/v1/articles/translation-needed?limit=1", nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		response := httptest.NewRecorder()
		copy := w
		copy.Public = public
		copy.ServeHTTP(response, r)
		return response
	}
	scoped := call(key, false)
	var result struct {
		Candidates []translationCandidate `json:"candidates"`
		Total      int                    `json:"total"`
	}
	if scoped.Code != 200 || json.Unmarshal(scoped.Body.Bytes(), &result) != nil || result.Total != 1 || len(result.Candidates) != 1 || result.Candidates[0].SourceID != "alpha" || strings.Contains(scoped.Body.String(), "beta") {
		t.Fatal("scope leaked or allowed item missing", scoped.Code)
	}
	if call("", false).Code != 404 || call(key, true).Code != 404 {
		t.Fatal("queue exposed anonymously")
	}
	if call("reader", false).Code != 200 {
		t.Fatal("reader cannot list queue")
	}
	if err = s.revokeAccessKey("agent"); err != nil {
		t.Fatal(err)
	}
	if call(key, false).Code != 404 {
		t.Fatal("revoked key still sees queue")
	}
}
