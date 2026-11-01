package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"unicode"
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
		{false, "", "# Перевод"}, {true, "length", "# Перевод"}, {true, "stop", "# Original"}, {true, "stop", "<think>analysis</think>\n# Перевод"},
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

func TestLocalTranslationWorkerProcessesLongDocumentBySection(t *testing.T) {
	s := testStore(t)
	if err := s.addSource(Source{ID: "program", Title: "Program", Kind: "program", Visibility: "private"}); err != nil {
		t.Fatal(err)
	}
	original := "# First\n\n" + strings.Repeat("hello ", 3000) + "\n\n# Second\n\n" + strings.Repeat("world ", 3000) + "\n\n```sh\necho hello\n```\n"
	if _, err := s.put("program", "docs/long.md", original, 0); err != nil {
		t.Fatal(err)
	}
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Prompt string `json:"prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		requests++
		output := strings.ReplaceAll(strings.ReplaceAll(input.Prompt, "# First", "# Первый"), "# Second", "# Второй")
		jsonResponse(w, 200, map[string]any{"done": true, "response": output})
	}))
	defer srv.Close()
	worker, err := newTranslationWorker(s, srv.URL, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	worker.Once(context.Background())
	a, err := s.article("program", "docs/long.md", false)
	if err != nil || requests != 2 || a.Translation == nil || !strings.Contains(a.Translation.Text, "# Второй") || !strings.Contains(a.Translation.Text, "echo hello") {
		t.Fatal("long document not translated safely", requests, err)
	}
}

func TestTranslationChunksDoNotSplitInsideFencesOrHTML(t *testing.T) {
	largeFence := "# Heading\n\n```text\n# Fake heading\n" + strings.Repeat("x", 33000) + "\n```\n"
	if _, err := translationChunks(largeFence, 32000); err == nil {
		t.Fatal("oversized code fence split")
	}
	largeHTML := "# Heading\n\n<div>\n" + strings.Repeat("x", 17000) + "\n\n# Fake heading\n" + strings.Repeat("x", 17000) + "\n</div>\n"
	if _, err := translationChunks(largeHTML, 32000); err == nil {
		t.Fatal("HTML-containing document split")
	}
}

func TestLocalMLXTranslationResponse(t *testing.T) {
	s := testStore(t)
	if err := s.addSource(Source{ID: "program", Title: "Program", Kind: "program", Visibility: "private"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("program", "docs/a.md", "# Setup `install`", 0); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Error("unexpected path")
		}
		var body struct {
			Messages []struct{ Role, Content string } `json:"messages"`
			Template map[string]bool                  `json:"chat_template_kwargs"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Messages) != 2 || body.Messages[1].Content != "# Setup `install`" || body.Template["enable_thinking"] {
			t.Error("invalid request")
		}
		jsonResponse(w, 200, map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"content": "# Установка `install`"}}}})
	}))
	defer srv.Close()
	worker, err := newTranslationWorkerProtocol(s, srv.URL, "fixture", "mlx")
	if err != nil {
		t.Fatal(err)
	}
	worker.Once(context.Background())
	a, err := s.article("program", "docs/a.md", false)
	if err != nil || a.Translation == nil || a.Translation.Text != "# Установка `install`" {
		t.Fatal("MLX translation not saved", err)
	}
}

// Opt-in synthetic acceptance against an explicitly supplied local MLX server.
func TestRealLocalMLXSyntheticTranslation(t *testing.T) {
	endpoint, model := os.Getenv("VEDA_TEST_MLX_URL"), os.Getenv("VEDA_TEST_MLX_MODEL")
	if endpoint == "" || model == "" {
		t.Skip("local MLX acceptance not configured")
	}
	s := testStore(t)
	if err := s.addSource(Source{ID: "program", Title: "Program", Kind: "program", Visibility: "private"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("program", "docs/a.md", "# Setup\n\nRun `install` and read [guide](guide.md).", 0); err != nil {
		t.Fatal(err)
	}
	worker, err := newTranslationWorkerProtocol(s, endpoint, model, "mlx")
	if err != nil {
		t.Fatal(err)
	}
	worker.Once(context.Background())
	a, err := s.article("program", "docs/a.md", false)
	if err != nil || a.Translation == nil || !strings.Contains(a.Translation.Text, "`install`") || !strings.Contains(a.Translation.Text, "(guide.md)") || !strings.ContainsFunc(a.Translation.Text, func(r rune) bool { return unicode.In(r, unicode.Cyrillic) }) {
		t.Fatal("local model did not produce a structurally valid translation", err)
	}
}
