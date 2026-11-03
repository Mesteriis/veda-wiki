package main

import (
	"encoding/json"
	"fmt"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestExcerptScopedUnicodeAndRevision(t *testing.T) {
	s := testStore(t)
	source(t, s, "alpha", "private")
	source(t, s, "beta", "private")
	original := "я😀alpha\nфинал"
	if _, err := s.put("alpha", "guide.md", original, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("beta", "guide.md", "# Other secret", 0); err != nil {
		t.Fatal(err)
	}
	key, err := s.createAccessKey("agent", []string{"alpha"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	w := Wiki{Store: s, Token: "owner"}
	call := func(query, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/api/v1/articles/excerpt?"+query, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		out := httptest.NewRecorder()
		w.ServeHTTP(out, req)
		return out
	}
	first := call("source_id=alpha&path=guide.md&limit=2", key)
	var page struct {
		Text  string `json:"text"`
		SHA   string `json:"sha256"`
		Next  *int   `json:"next_offset"`
		Total int    `json:"total_characters"`
	}
	if first.Code != 200 || json.Unmarshal(first.Body.Bytes(), &page) != nil || page.Text != "я😀" || page.Next == nil || *page.Next != 2 || page.Total != len([]rune(original)) {
		t.Fatal("first slice invalid", first.Code)
	}
	next := call(fmt.Sprintf("source_id=alpha&path=guide.md&offset=2&limit=16000&expected_sha256=%s", page.SHA), key)
	var tail struct {
		Text string `json:"text"`
		Next *int   `json:"next_offset"`
	}
	if next.Code != 200 || json.Unmarshal(next.Body.Bytes(), &tail) != nil || tail.Text != "alpha\nфинал" || tail.Next != nil {
		t.Fatal("continuation invalid", next.Code)
	}
	if call("source_id=alpha&path=guide.md&offset=2", key).Code != 400 || call("source_id=alpha&path=guide.md&limit=16001", key).Code != 400 {
		t.Fatal("invalid continuation accepted")
	}
	if call("source_id=beta&path=guide.md", key).Code != 404 || call("source_id=alpha&path=guide.md", "").Code != 404 {
		t.Fatal("private scope escaped")
	}
	if _, err = s.put("alpha", "guide.md", "# Changed", 1); err != nil {
		t.Fatal(err)
	}
	if call(fmt.Sprintf("source_id=alpha&path=guide.md&offset=2&expected_sha256=%s", page.SHA), key).Code != 409 {
		t.Fatal("stale continuation accepted")
	}
}

func TestExcerptPublicUsesPublishedRevision(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "# Published"})
	connectFixture(t, s, f, false)
	s.mu.Lock()
	a := s.db.Articles["handbook/guide.md"]
	text := "# Private draft"
	a.Versions = append(a.Versions, Revision{Number: 2, Text: text, SHA: digest([]byte(text))})
	s.db.Articles["handbook/guide.md"] = a
	s.mu.Unlock()
	out := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/articles/excerpt?source_id=handbook&path=guide.md", nil)
	(Wiki{Store: s, Public: true}).ServeHTTP(out, req)
	if out.Code != 200 || !strings.Contains(out.Body.String(), "Published") || strings.Contains(out.Body.String(), "Private draft") {
		t.Fatal("public excerpt exposed a draft", out.Code)
	}
}

func TestDisplayExcerptTranslationScopeAndRevision(t *testing.T) {
	s := testStore(t)
	for _, id := range []string{"alpha", "beta"} {
		if err := s.addSource(Source{ID: id, Title: id, Kind: "program", Visibility: "private"}); err != nil {
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
	in := translationRequest{SourceID: "alpha", Path: "docs/setup.md", ExpectedRevision: 1, OriginalSHA: a.Versions[0].SHA, Text: "# Установка 😀"}
	if _, err = s.submitTranslation(in); err != nil {
		t.Fatal(err)
	}
	key, err := s.createAccessKey("agent", []string{"alpha"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	call := func(query string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/api/v1/articles/display-excerpt?"+query, nil)
		req.Header.Set("Authorization", "Bearer "+key)
		out := httptest.NewRecorder()
		(Wiki{Store: s, Token: "owner"}).ServeHTTP(out, req)
		return out
	}
	var first struct {
		Text       string `json:"text"`
		SHA        string `json:"sha256"`
		Original   string `json:"original_sha256"`
		Next       *int   `json:"next_offset"`
		Translated bool   `json:"translated"`
	}
	r := call("source_id=alpha&path=docs%2Fsetup.md&limit=3")
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &first) != nil || first.Text != "# У" || first.Next == nil || *first.Next != 3 || !first.Translated || first.Original != in.OriginalSHA {
		t.Fatal("invalid first slice", r.Code)
	}
	query := fmt.Sprintf("source_id=alpha&path=docs%%2Fsetup.md&offset=3&expected_sha256=%s&expected_original_sha256=%s", first.SHA, first.Original)
	tail := call(query)
	if tail.Code != 200 || !strings.Contains(tail.Body.String(), "становка 😀") {
		t.Fatal("invalid continuation", tail.Code)
	}
	if call("source_id=alpha&path=docs%2Fsetup.md&offset=3&expected_sha256="+first.SHA).Code != 400 || call("source_id=beta&path=docs%2Fsetup.md").Code != 404 {
		t.Fatal("missing revision or scope bypass")
	}
	in.Text = "# Настройка 😀"
	if _, err = s.submitTranslation(in); err != nil {
		t.Fatal(err)
	}
	if call(query).Code != 409 {
		t.Fatal("changed translation accepted")
	}
	var nextFirst struct {
		SHA      string `json:"sha256"`
		Original string `json:"original_sha256"`
	}
	if err = json.Unmarshal(call("source_id=alpha&path=docs%2Fsetup.md&limit=3").Body.Bytes(), &nextFirst); err != nil {
		t.Fatal(err)
	}
	sameTextQuery := fmt.Sprintf("source_id=alpha&path=docs%%2Fsetup.md&offset=3&expected_sha256=%s&expected_original_sha256=%s", nextFirst.SHA, nextFirst.Original)
	if _, err = s.put("alpha", "docs/setup.md", in.Text, 1); err != nil {
		t.Fatal(err)
	}
	if call(sameTextQuery).Code != 409 {
		t.Fatal("changed original accepted even though displayed bytes match")
	}
}
