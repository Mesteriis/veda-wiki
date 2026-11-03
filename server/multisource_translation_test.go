package main

import (
	"strings"
	"testing"
)

func TestTranslationsStayBoundToSourceAndOriginalAfterRestart(t *testing.T) {
	s := testStore(t)
	for _, fixture := range []struct{ id, visibility, translation string }{
		{"alpha", "public", "# Общая установка\n\nЗапустите `install`."},
		{"beta", "private", "# Локальная установка\n\nВыполните `install`."},
	} {
		batch := testBatch()
		batch.SourceID, batch.Kind, batch.Visibility = fixture.id, "program", fixture.visibility
		batch.Metadata = nil
		batch.Files = map[string]string{"docs/guide.md": "# Setup\n\nRun `install`."}
		if _, err := s.importBatch(batch); err != nil {
			t.Fatal(err)
		}
		article, err := s.article(fixture.id, "docs/guide.md", false)
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.submitTranslation(translationRequest{
			SourceID: fixture.id, Path: "docs/guide.md", ExpectedRevision: 1,
			OriginalSHA: article.Versions[0].SHA, Text: fixture.translation,
			ConfirmPublication: fixture.visibility == "public",
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := migrateSQLite(s.dir); err != nil {
		t.Fatal(err)
	}
	var err error
	s, err = openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if page, err := s.page("alpha/docs/guide.md", true); err != nil || !page.Translated || !strings.Contains(page.Text, "Общая") {
		t.Fatal("public translation missing after restart", err)
	}
	if _, err := s.page("beta/docs/guide.md", true); err != ErrNotFound {
		t.Fatal("private translation exposed", err)
	}
	if page, err := s.page("beta/docs/guide.md", false); err != nil || !page.Translated || !strings.Contains(page.Text, "Локальная") {
		t.Fatal("private source translation missing", err)
	}
	updated := testBatch()
	updated.SourceID, updated.Kind, updated.Metadata = "alpha", "program", nil
	updated.Files = map[string]string{"docs/guide.md": "# Updated setup\n\nRun `install`."}
	updated.Commit = strings.Repeat("b", 40)
	updated = applyAgain(s, updated)
	if result, err := s.importBatch(updated); err != nil || result.Updated != 1 {
		t.Fatal("changed upstream document was not imported", result, err)
	}
	s, err = openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if page, err := s.page("alpha/docs/guide.md", true); err != nil || page.Translated || !strings.Contains(page.Text, "Updated setup") {
		t.Fatal("stale translation remained public", err)
	}
	if page, err := s.page("beta/docs/guide.md", false); err != nil || !page.Translated || !strings.Contains(page.Text, "Локальная") {
		t.Fatal("change in first source affected the second translation", err)
	}
}
