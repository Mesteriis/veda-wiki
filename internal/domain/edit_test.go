package domain

import (
	"errors"
	"testing"
	"time"
)

func TestReplaceFragmentNeedsExactlyOneMatch(t *testing.T) {
	got, err := ReplaceFragment("# A\nold line\nend\n", "old line", "new line")
	if err != nil || got != "# A\nnew line\nend\n" {
		t.Fatalf("replace %q %v", got, err)
	}
	if _, err := ReplaceFragment("# A\n", "missing", "x"); !errors.Is(err, ErrFragmentNotFound) || !errors.Is(err, ErrInvalid) {
		t.Fatal("missing fragment", err)
	}
	if _, err := ReplaceFragment("x x", "x", "y"); !errors.Is(err, ErrFragmentAmbiguous) || !errors.Is(err, ErrInvalid) {
		t.Fatal("ambiguous fragment", err)
	}
	if _, err := ReplaceFragment("abc", "", "y"); !errors.Is(err, ErrInvalid) {
		t.Fatal("empty fragment", err)
	}
	// Overlapping occurrences count as two.
	if _, err := ReplaceFragment("aaa", "aa", "b"); !errors.Is(err, ErrFragmentAmbiguous) {
		t.Fatal("overlapping fragment", err)
	}
	if got, err := ReplaceFragment("keep this", " this", ""); err != nil || got != "keep" {
		t.Fatalf("deletion %q %v", got, err)
	}
}

func TestEditArticleWritesANewRevisionFromTheCurrentText(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	db := Database{Sources: map[string]Source{"one": {ID: "one", Title: "One", Kind: "wiki", Visibility: "private"}}, Articles: map[string]Article{}}
	if _, _, err := PutArticle(&db, "one", "a.md", "# A\nalpha\n", 0, now); err != nil {
		t.Fatal(err)
	}
	rev, key, err := EditArticleVia(&db, "one", "a.md", "alpha", "beta", 1, "mcp-editor", now)
	if err != nil || key != "one/a.md" || rev.Number != 2 || rev.Text != "# A\nbeta\n" || rev.Via != "mcp-editor" {
		t.Fatal("edit", rev, key, err)
	}
	if _, _, err := EditArticleVia(&db, "one", "a.md", "beta", "gamma", 1, "", now); !errors.Is(err, ErrConflict) {
		t.Fatal("stale revision must conflict before fragment matching", err)
	}
	if _, _, err := EditArticleVia(&db, "one", "a.md", "alpha", "x", 2, "", now); !errors.Is(err, ErrFragmentNotFound) {
		t.Fatal("fragment of the old revision", err)
	}
	if _, _, err := EditArticleVia(&db, "one", "new.md", "a", "b", 1, "", now); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing article", err)
	}
	deleted := db.Articles["one/a.md"]
	deleted.Deleted = true
	db.Articles["one/a.md"] = deleted
	if _, _, err := EditArticleVia(&db, "one", "a.md", "beta", "x", 2, "", now); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted article", err)
	}
	if len(db.Articles["one/a.md"].Versions) != 2 {
		t.Fatal("refused edits must not write")
	}
}
