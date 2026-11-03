package domain

import (
	"errors"
	"testing"
	"time"
)

func TestPutArticleNarrowsPersistenceUnlessAMoveIsPending(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	db := Database{Sources: map[string]Source{"one": {ID: "one", Title: "One", Kind: "wiki", Visibility: "private"}}, Articles: map[string]Article{}, Moves: map[string]Move{"done": {State: "complete"}}}
	rev, key, err := PutArticle(&db, "one", "a.md", "# A", 0, now)
	if err != nil || key != "one/a.md" || rev.Number != 1 || db.Articles["one/a.md"].Versions[0].SHA != rev.SHA {
		t.Fatal("first revision", key, err)
	}
	if _, _, err := PutArticle(&db, "one", "a.md", "# Stale", 0, now); !errors.Is(err, ErrConflict) {
		t.Fatal("stale expected revision accepted", err)
	}
	db.Moves["open"] = Move{State: "pending_delete"}
	if _, key, err = PutArticle(&db, "one", "b.md", "# B", 0, now); err != nil || key != "" {
		t.Fatal("a pending move must persist the whole state", key, err)
	}
}

func TestAddSourceRefusesImportFieldsAndDuplicates(t *testing.T) {
	db := Database{Sources: map[string]Source{}}
	src := Source{ID: "one", Title: "One", Kind: "wiki", Visibility: "public"}
	if err := AddSource(&db, src); err != nil {
		t.Fatal(err)
	}
	if err := AddSource(&db, src); !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate accepted", err)
	}
	imported := Source{ID: "two", Title: "Two", Kind: "wiki", Visibility: "public", ImportCommit: "abc"}
	if err := AddSource(&db, imported); !errors.Is(err, ErrInvalid) {
		t.Fatal("import field accepted", err)
	}
}
