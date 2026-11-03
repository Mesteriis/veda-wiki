package main

import (
	"context"
	"encoding/json"
	"github.com/Mesteriis/veda-wiki/internal/translation"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLocalTranslationWorkerSavesPrivateDraft(t *testing.T) {
	s := testStore(t)
	if err := s.addSource(Source{ID: "program", Title: "Program", Kind: "program", Visibility: "public"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("program", "docs/guide.md", "# Setup\n\nRun `install`.", 0); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" || r.Method != "POST" {
			t.Error("unexpected request")
		}
		var input struct {
			Model  string `json:"model"`
			Prompt string `json:"prompt"`
			Stream bool   `json:"stream"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil || input.Model != "fixture" || input.Prompt != "# Setup\n\nRun `install`." || input.Stream {
			t.Error("unexpected input")
		}
		jsonResponse(w, 200, map[string]any{"done": true, "done_reason": "stop", "response": "# Установка\n\nЗапустите `install`."})
	}))
	defer srv.Close()
	worker, err := translation.NewWorker(s.Store, srv.URL, "fixture", "ollama")
	if err != nil {
		t.Fatal(err)
	}
	worker.Once(context.Background())
	private, err := s.page("program/docs/guide.md", false)
	if err != nil || !private.Translated || private.Text != "# Установка\n\nЗапустите `install`." {
		t.Fatal("private draft missing", err)
	}
	public, err := s.page("program/docs/guide.md", true)
	if err != nil || public.Translated || public.Text != "# Setup\n\nRun `install`." {
		t.Fatal("unapproved translation published", err)
	}
	if candidates := s.translation().Candidates(); len(candidates) != 1 || candidates[0].State != "publication_review" {
		t.Fatal("missing review", candidates)
	}
}

func TestLocalTranslationOriginSurvivesRestartAndApproval(t *testing.T) {
	s := testStore(t)
	programSource(t, s, "program", "public")
	rev, err := s.put("program", "docs/guide.md", "# Setup", 0)
	if err != nil {
		t.Fatal(err)
	}
	in := translationRequest{SourceID: "program", Path: "docs/guide.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Установка"}
	if _, err = s.translation().SaveDraft(in); err != nil {
		t.Fatal(err)
	}
	s, err = openStore(s.Dir())
	if err != nil || s.DB().Version != 11 {
		t.Fatal("origin store failed to reopen", err)
	}
	private, err := s.page("program/docs/guide.md", false)
	if err != nil || private.TranslationOrigin != "local_model" {
		t.Fatal("draft origin lost", err)
	}
	view := request(Wiki{Store: s, Token: "test-owner"}, "GET", "/api/state?page=program%2Fdocs%2Fguide.md", "", true)
	var response struct {
		View struct {
			Page *Page `json:"Page"`
		} `json:"view"`
	}
	if view.Code != 200 || json.Unmarshal(view.Body.Bytes(), &response) != nil || response.View.Page == nil || response.View.Page.TranslationOrigin != "local_model" {
		t.Fatal("reader API lost translation origin")
	}
	public, err := s.page("program/docs/guide.md", true)
	if err != nil || public.Translated || public.TranslationOrigin != "" {
		t.Fatal("draft origin exposed", err)
	}
	in.ConfirmPublication = true
	if _, err = s.translation().Submit(in); err != nil {
		t.Fatal(err)
	}
	public, err = s.page("program/docs/guide.md", true)
	if err != nil || !public.Translated || public.TranslationOrigin != "local_model" {
		t.Fatal("approval lost origin", err)
	}
	in.Text = "# Настройка"
	if _, err = s.translation().Submit(in); err != nil {
		t.Fatal(err)
	}
	public, err = s.page("program/docs/guide.md", true)
	if err != nil || public.TranslationOrigin != "" {
		t.Fatal("edited translation incorrectly attributed", err)
	}
}

func TestLocalTranslationOriginSurvivesMove(t *testing.T) {
	s := testStore(t)
	programSource(t, s, "from", "private")
	programSource(t, s, "to", "public")
	rev, err := s.put("from", "docs/a.md", "# Original", 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.translation().SaveDraft(translationRequest{SourceID: "from", Path: "docs/a.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Перевод"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.moveArticle(moveRequest{SourceID: "from", SourcePath: "docs/a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "docs/b.md", ConfirmMove: true, ConfirmPublication: true})
	if err != nil {
		t.Fatal(err)
	}
	s, err = openStore(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	private, err := s.page("to/docs/b.md", false)
	if err != nil || private.TranslationOrigin != "local_model" {
		t.Fatal("move lost origin", err)
	}
	public, err := s.page("to/docs/b.md", true)
	if err != nil || public.Translated || public.TranslationOrigin != "" {
		t.Fatal("move published draft", err)
	}
}
