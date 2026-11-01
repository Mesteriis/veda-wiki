package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
	worker, err := newTranslationWorker(s, srv.URL, "fixture")
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
	if candidates := s.translationCandidates(); len(candidates) != 1 || candidates[0].State != "publication_review" {
		t.Fatal("missing review", candidates)
	}
}

func TestLocalTranslationWorkerRejectsStaleOrAlteredCode(t *testing.T) {
	for _, stale := range []bool{false, true} {
		s := testStore(t)
		if err := s.addSource(Source{ID: "program", Title: "Program", Kind: "program", Visibility: "private"}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.put("program", "docs/a.md", "# Run `install`", 0); err != nil {
			t.Fatal(err)
		}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if stale {
				if _, err := s.put("program", "docs/a.md", "# Changed `install`", 1); err != nil {
					t.Error(err)
				}
			}
			jsonResponse(w, 200, map[string]any{"done": true, "response": "# Запуск `wrong`"})
		}))
		worker, err := newTranslationWorker(s, srv.URL, "fixture")
		if err != nil {
			t.Fatal(err)
		}
		worker.Once(context.Background())
		srv.Close()
		a, err := s.article("program", "docs/a.md", false)
		if err != nil || a.Translation != nil {
			t.Fatal("invalid translation saved", err)
		}
	}
}

func TestLocalTranslationWorkerRejectsRemoteOrAmbiguousEndpoints(t *testing.T) {
	for _, endpoint := range []string{"http://localhost:11434", "http://192.0.2.1:11434", "https://127.0.0.1:11434", "http://127.0.0.1:11434/path", "http://user@127.0.0.1:11434"} {
		if _, err := newTranslationWorker(testStore(t), endpoint, "model"); err == nil {
			t.Fatalf("accepted %s", endpoint)
		}
	}
}

func TestLocalTranslationWorkerDoesNotOverwriteConcurrentOwnerTranslation(t *testing.T) {
	s := testStore(t)
	if err := s.addSource(Source{ID: "program", Title: "Program", Kind: "program", Visibility: "private"}); err != nil {
		t.Fatal(err)
	}
	rev, err := s.put("program", "docs/a.md", "# Original", 0)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := s.submitTranslation(translationRequest{SourceID: "program", Path: "docs/a.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Перевод владельца"})
		if err != nil {
			t.Error(err)
		}
		jsonResponse(w, 200, map[string]any{"done": true, "response": "# Машинный перевод"})
	}))
	defer srv.Close()
	worker, err := newTranslationWorker(s, srv.URL, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	worker.Once(context.Background())
	a, err := s.article("program", "docs/a.md", false)
	if err != nil || a.Translation == nil || a.Translation.Text != "# Перевод владельца" {
		t.Fatal("owner translation overwritten", err)
	}
}

func TestLocalTranslationWorkerRejectsIncompleteAndUnchangedOutput(t *testing.T) {
	for _, fixture := range []struct {
		done           bool
		reason, output string
	}{
		{false, "", "# Перевод"}, {true, "length", "# Перевод"}, {true, "stop", "# Original"},
	} {
		s := testStore(t)
		if err := s.addSource(Source{ID: "program", Title: "Program", Kind: "program", Visibility: "private"}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.put("program", "docs/a.md", "# Original", 0); err != nil {
			t.Fatal(err)
		}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			jsonResponse(w, 200, map[string]any{"done": fixture.done, "done_reason": fixture.reason, "response": fixture.output})
		}))
		worker, err := newTranslationWorker(s, srv.URL, "fixture")
		if err != nil {
			t.Fatal(err)
		}
		worker.Once(context.Background())
		srv.Close()
		a, err := s.article("program", "docs/a.md", false)
		if err != nil || a.Translation != nil {
			t.Fatal("incomplete or unchanged output accepted", fixture, err)
		}
	}
}

func TestOversizedDocumentDoesNotBlockOtherTranslations(t *testing.T) {
	s := testStore(t)
	if err := s.addSource(Source{ID: "program", Title: "Program", Kind: "program", Visibility: "private"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("program", "docs/a.md", strings.Repeat("a", 32001), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("program", "docs/b.md", "# Second", 0); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, 200, map[string]any{"done": true, "response": "# Вторая"})
	}))
	defer srv.Close()
	worker, err := newTranslationWorker(s, srv.URL, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	worker.Once(context.Background())
	a, err := s.article("program", "docs/b.md", false)
	if err != nil || a.Translation == nil || a.Translation.Text != "# Вторая" {
		t.Fatal("shorter article blocked", err)
	}
}
