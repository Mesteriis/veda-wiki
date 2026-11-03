package main

import (
	"context"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
	"github.com/Mesteriis/veda-wiki/internal/translation"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProgramImportTranslationAndUpstreamChange(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"README.md": "# Not imported", "docs/setup.md": "# Setup\n\nRun `install`.", "src/main.go": "package example"})
	f.Modify(func() { f.Private = true })
	manager := connectFixture(t, s, f, true)
	if len(s.translation().Candidates()) != 1 {
		t.Fatal("import did not enqueue documentation")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, 200, map[string]any{"done": true, "response": "# Установка\n\nЗапустите `install`."})
	}))
	defer srv.Close()
	worker, err := translation.NewWorker(s.Store, srv.URL, "synthetic", "ollama")
	if err != nil {
		t.Fatal(err)
	}
	worker.Once(context.Background())
	page, err := s.page("handbook/docs/setup.md", false)
	if err != nil || !page.Translated {
		t.Fatal("translation not visible", err)
	}
	if _, err = s.article("handbook", "README.md", false); err != ErrNotFound {
		t.Fatal("non-docs content imported", err)
	}
	f.Advance(map[string]string{"README.md": "# Not imported", "docs/setup.md": "# Setup revised\n\nRun `install`.", "src/main.go": "package example"})
	syncFixtureSource(t, manager, "handbook")
	page, err = s.page("handbook/docs/setup.md", false)
	if err != nil || page.Translated || page.Text != "# Setup revised\n\nRun `install`." {
		t.Fatal("stale translation shown", err)
	}
	candidates := s.translation().Candidates()
	if len(candidates) != 1 || candidates[0].State != "needed" || candidates[0].Revision != 2 {
		t.Fatal("updated original not requeued", candidates)
	}
}
