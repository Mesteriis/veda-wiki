package translation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

func TestLocalTranslationWorkerRejectsStaleOrAlteredCode(t *testing.T) {
	for _, stale := range []bool{false, true} {
		s := newFixture(t)
		if err := s.addSource(domain.Source{ID: "program", Title: "Program", Kind: "program", Visibility: "private"}); err != nil {
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
		worker, err := NewWorker(s.Store, srv.URL, "fixture", "ollama")
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
		if _, err := NewWorker(newFixture(t).Store, endpoint, "model", "ollama"); err == nil {
			t.Fatalf("accepted %s", endpoint)
		}
	}
}

func TestLocalTranslationWorkerDoesNotOverwriteConcurrentOwnerTranslation(t *testing.T) {
	s := newFixture(t)
	if err := s.addSource(domain.Source{ID: "program", Title: "Program", Kind: "program", Visibility: "private"}); err != nil {
		t.Fatal(err)
	}
	rev, err := s.put("program", "docs/a.md", "# Original", 0)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := s.Submit(Request{SourceID: "program", Path: "docs/a.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Перевод владельца"})
		if err != nil {
			t.Error(err)
		}
		jsonResponse(w, 200, map[string]any{"done": true, "response": "# Машинный перевод"})
	}))
	defer srv.Close()
	worker, err := NewWorker(s.Store, srv.URL, "fixture", "ollama")
	if err != nil {
		t.Fatal(err)
	}
	worker.Once(context.Background())
	a, err := s.article("program", "docs/a.md", false)
	if err != nil || a.Translation == nil || a.Translation.Text != "# Перевод владельца" {
		t.Fatal("owner translation overwritten", err)
	}
}

func TestLocalTranslationWorkerStopsRemainingChunksWhenWorkBecomesStale(t *testing.T) {
	for _, change := range []string{"none", "revision", "owner", "deleted", "conflict"} {
		t.Run(change, func(t *testing.T) {
			s := newFixture(t)
			programSource(t, s, "program", "private")
			original := "# Setup\n\n" + strings.Repeat("English ", 2500) + "\n\n" + strings.Repeat("Second ", 2500)
			rev, err := s.put("program", "docs/guide.md", original, 0)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var input struct {
					Prompt string `json:"prompt"`
				}
				if json.NewDecoder(r.Body).Decode(&input) != nil {
					t.Error("invalid request")
					return
				}
				if calls == 1 {
					var changeErr error
					switch change {
					case "revision":
						_, changeErr = s.put("program", "docs/guide.md", "# New revision", 1)
					case "owner":
						_, changeErr = s.Submit(Request{SourceID: "program", Path: "docs/guide.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Ручной перевод\n\nТекст"})
					case "deleted", "conflict":
						s.mutate(func(db *domain.Database) {
							a := db.Articles["program/docs/guide.md"]
							if change == "deleted" {
								a.Deleted = true
							} else {
								a.Conflicts = append(a.Conflicts, domain.ImportConflict{})
							}
							db.Articles["program/docs/guide.md"] = a
						})
					}
					if changeErr != nil {
						t.Error(changeErr)
					}
				}
				translated := "Текст"
				if strings.HasPrefix(input.Prompt, "#") {
					translated = "# Установка\n\nТекст"
				}
				jsonResponse(w, 200, map[string]any{"done": true, "response": translated})
			}))
			defer srv.Close()
			worker, err := NewWorker(s.Store, srv.URL, "fixture", "ollama")
			if err != nil {
				t.Fatal(err)
			}
			worker.Once(context.Background())
			a, err := s.article("program", "docs/guide.md", false)
			if err != nil && !(change == "deleted" && errors.Is(err, domain.ErrNotFound)) {
				t.Fatal(err)
			}
			if change == "none" {
				if calls < 2 || a.Translation == nil || a.Translation.Origin != "local_model" {
					t.Fatal("unchanged document did not finish translation", calls)
				}
			} else if calls != 1 || (change == "owner" && (a.Translation == nil || a.Translation.Text != "# Ручной перевод\n\nТекст")) || (change != "owner" && a.Translation != nil) {
				t.Fatal("stale job continued or replaced a translation", calls)
			}
		})
	}
}

func TestLocalTranslationWorkerRejectsIncompleteAndUnchangedOutput(t *testing.T) {
	for _, fixture := range []struct {
		done           bool
		reason, output string
	}{
		{false, "", "# Перевод"}, {true, "length", "# Перевод"}, {true, "stop", "# Original"}, {true, "stop", "<think>analysis</think>\n# Перевод"},
	} {
		s := newFixture(t)
		if err := s.addSource(domain.Source{ID: "program", Title: "Program", Kind: "program", Visibility: "private"}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.put("program", "docs/a.md", "# Original", 0); err != nil {
			t.Fatal(err)
		}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			jsonResponse(w, 200, map[string]any{"done": fixture.done, "done_reason": fixture.reason, "response": fixture.output})
		}))
		worker, err := NewWorker(s.Store, srv.URL, "fixture", "ollama")
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
	s := newFixture(t)
	if err := s.addSource(domain.Source{ID: "program", Title: "Program", Kind: "program", Visibility: "private"}); err != nil {
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
	worker, err := NewWorker(s.Store, srv.URL, "fixture", "ollama")
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
	s := newFixture(t)
	if err := s.addSource(domain.Source{ID: "program", Title: "Program", Kind: "program", Visibility: "private"}); err != nil {
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
		output = strings.ReplaceAll(strings.ReplaceAll(output, "hello ", "привет "), "world ", "мир ")
		jsonResponse(w, 200, map[string]any{"done": true, "response": output})
	}))
	defer srv.Close()
	worker, err := NewWorker(s.Store, srv.URL, "fixture", "ollama")
	if err != nil {
		t.Fatal(err)
	}
	worker.Once(context.Background())
	a, err := s.article("program", "docs/long.md", false)
	if err != nil || requests != 2 || a.Translation == nil || !strings.Contains(a.Translation.Text, "# Второй") || !strings.Contains(a.Translation.Text, "echo hello") {
		t.Fatal("long document not translated safely", requests, err)
	}
}

func TestLocalTranslationWorkerRejectsNonRussianOutput(t *testing.T) {
	s := newFixture(t)
	if err := s.addSource(domain.Source{ID: "program", Title: "Program", Kind: "program", Visibility: "private"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("program", "docs/a.md", "# Setup\n\nRun `install`.", 0); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, 200, map[string]any{"done": true, "response": "# Installation\n\nRun `install` now."})
	}))
	defer srv.Close()
	worker, err := NewWorker(s.Store, srv.URL, "fixture", "ollama")
	if err != nil {
		t.Fatal(err)
	}
	worker.Once(context.Background())
	a, err := s.article("program", "docs/a.md", false)
	if err != nil || a.Translation != nil || len(worker.retry) != 1 {
		t.Fatal("non-Russian output was accepted", err)
	}
}

func TestLocalWorkerTranslatesLargeParagraphs(t *testing.T) {
	s := newFixture(t)
	programSource(t, s, "program", "private")
	original := strings.Repeat("Hello ", 3000) + "\n\n```sh\necho safe\n```\n\n" + strings.Repeat("Hello ", 3000) + "\n\n<div>Keep HTML text</div>\n"
	if _, err := s.put("program", "docs/guide.md", original, 0); err != nil {
		t.Fatal(err)
	}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Prompt string `json:"prompt"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil {
			t.Error("invalid request")
			return
		}
		calls++
		jsonResponse(w, 200, map[string]any{"done": true, "done_reason": "stop", "response": strings.ReplaceAll(input.Prompt, "Hello", "Привет")})
	}))
	defer srv.Close()
	worker, err := NewWorker(s.Store, srv.URL, "fixture", "ollama")
	if err != nil {
		t.Fatal(err)
	}
	worker.Once(context.Background())
	a, err := s.article("program", "docs/guide.md", false)
	if err != nil || calls < 2 || a.Translation == nil || a.Translation.Origin != "local_model" || !reflect.DeepEqual(protectedMarkdown(original), protectedMarkdown(a.Translation.Text)) {
		t.Fatal("large paragraph translation failed", err)
	}
}

func TestLocalTranslationWorkerRejectsLostListStep(t *testing.T) {
	s := newFixture(t)
	programSource(t, s, "program", "private")
	if _, err := s.put("program", "docs/steps.md", "# Setup\n\n1. First\n2. Second\n", 0); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, 200, map[string]any{"done": true, "response": "# Установка\n\n1. Первый\n"})
	}))
	defer srv.Close()
	worker, err := NewWorker(s.Store, srv.URL, "fixture", "ollama")
	if err != nil {
		t.Fatal(err)
	}
	worker.Once(context.Background())
	article, err := s.article("program", "docs/steps.md", false)
	if err != nil || article.Translation != nil || len(worker.retry) != 1 {
		t.Fatal("incomplete list was saved", err)
	}
}

func TestLocalTranslationBearerIsPrivateRotatableAndNeverRedirected(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenPath, []byte("synthetic-one"), 0600); err != nil {
		t.Fatal(err)
	}
	expected := "Bearer synthetic-one"
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != expected {
			t.Error("wrong local bearer")
		}
		jsonResponse(w, 200, map[string]any{"done": true, "response": "# Перевод"})
	}))
	defer srv.Close()
	worker, err := NewWorker(newFixture(t).Store, srv.URL, "fixture", "ollama")
	if err != nil {
		t.Fatal(err)
	}
	worker.TokenFile = tokenPath
	if _, err = worker.translate(context.Background(), "# Original"); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(tokenPath, []byte("synthetic-two"), 0600); err != nil {
		t.Fatal(err)
	}
	expected = "Bearer synthetic-two"
	if _, err = worker.translate(context.Background(), "# Original"); err != nil || calls != 2 {
		t.Fatal("rotation failed", err)
	}
	if err = os.Chmod(tokenPath, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = worker.translate(context.Background(), "# Original"); err == nil || calls != 2 {
		t.Fatal("insecure file accepted")
	}
	if err = os.Chmod(tokenPath, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err = os.Symlink(tokenPath, link); err != nil {
		t.Fatal(err)
	}
	worker.TokenFile = link
	if _, err = worker.translate(context.Background(), "# Original"); err == nil || calls != 2 {
		t.Fatal("symlink accepted")
	}
	worker.TokenFile = tokenPath
	if err = os.WriteFile(tokenPath, []byte("synthetic\ninvalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = worker.translate(context.Background(), "# Original"); err == nil || calls != 2 {
		t.Fatal("invalid bearer accepted")
	}
	if err = os.WriteFile(tokenPath, []byte("synthetic-two"), 0600); err != nil {
		t.Fatal(err)
	}
	redirected := false
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected = true; w.WriteHeader(200) }))
	defer receiver.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, receiver.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	worker, err = NewWorker(newFixture(t).Store, redirect.URL, "fixture", "ollama")
	if err != nil {
		t.Fatal(err)
	}
	worker.TokenFile = tokenPath
	if _, err = worker.translate(context.Background(), "# Original"); err == nil || redirected {
		t.Fatal("redirect sent local bearer")
	}
}
