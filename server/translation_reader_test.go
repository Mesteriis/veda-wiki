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

func TestDisplayArticleTranslationVisibilityAndStaleness(t *testing.T) {
	s := testStore(t)
	for _, id := range []string{"alpha", "beta"} {
		if err := s.addSource(Source{ID: id, Title: id, Kind: "program", Visibility: "public"}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.put(id, "docs/setup.md", "# Setup", 0); err != nil {
			t.Fatal(err)
		}
	}
	a, err := s.article("alpha", "docs/setup.md", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.saveTranslation(translationRequest{SourceID: "alpha", Path: "docs/setup.md", ExpectedRevision: 1, OriginalSHA: a.Versions[0].SHA, Text: "# Установка"}, true); err != nil {
		t.Fatal(err)
	}
	key, err := s.createAccessKey("agent", []string{"alpha"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	call := func(id, token string, public bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/api/v1/articles/display?source_id="+id+"&path=docs%2Fsetup.md", nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		out := httptest.NewRecorder()
		(Wiki{Store: s, Token: "owner", Public: public}).ServeHTTP(out, r)
		return out
	}
	var private map[string]any
	r := call("alpha", key, false)
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &private) != nil || private["text"] != "# Установка" || private["translation_state"] != "current" || private["original_sha256"] != a.Versions[0].SHA {
		t.Fatal("scoped translation missing", r.Code)
	}
	public := call("alpha", "", true)
	if public.Code != 200 || strings.Contains(public.Body.String(), "Установка") || !strings.Contains(public.Body.String(), `"translation_state":"unavailable"`) {
		t.Fatal("public response exposed private translation", public.Code)
	}
	if call("beta", key, false).Code != 404 {
		t.Fatal("display escaped scope")
	}
	b, err := s.article("beta", "docs/setup.md", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.submitTranslation(translationRequest{SourceID: "beta", Path: "docs/setup.md", ExpectedRevision: 1, OriginalSHA: b.Versions[0].SHA, Text: "# Установка", ConfirmPublication: true}); err != nil {
		t.Fatal(err)
	}
	approved := call("beta", "", true)
	if approved.Code != 200 || !strings.Contains(approved.Body.String(), "Установка") || !strings.Contains(approved.Body.String(), `"translation_state":"current"`) {
		t.Fatal("approved public translation unavailable", approved.Code)
	}
	if _, err = s.put("alpha", "docs/setup.md", "# Changed", 1); err != nil {
		t.Fatal(err)
	}
	stale := call("alpha", key, false)
	if stale.Code != 200 || strings.Contains(stale.Body.String(), "Установка") || !strings.Contains(stale.Body.String(), `"translation_state":"stale"`) || !strings.Contains(stale.Body.String(), "# Changed") {
		t.Fatal("stale translation displayed", stale.Code)
	}
}
