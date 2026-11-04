package domain

import (
	"errors"
	"testing"
	"time"
)

func gappedArticle() Article {
	return Article{SourceID: "one", Path: "a.md", Versions: []Revision{{Number: 7, Text: "# 7"}, {Number: 8, Text: "# 8"}, {Number: 9, Text: "# 9"}}}
}

func TestRevisionLookupByNumber(t *testing.T) {
	a := gappedArticle()
	if LatestRevision(a) != 9 {
		t.Fatal("latest", LatestRevision(a))
	}
	if r, ok := RevisionByNumber(a, 8); !ok || r.Text != "# 8" {
		t.Fatal("revision 8", r, ok)
	}
	if _, ok := RevisionByNumber(a, 3); ok {
		t.Fatal("pruned revision found")
	}
	if r, ok := LastRevision(a); !ok || r.Number != 9 {
		t.Fatal("last", r, ok)
	}
	if LatestRevision(Article{}) != 0 {
		t.Fatal("empty article")
	}
	if _, ok := LastRevision(Article{}); ok {
		t.Fatal("empty article has a last revision")
	}
}

func TestPutArticleUsesLatestRevisionNumber(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	db := Database{Sources: map[string]Source{"one": {ID: "one", Title: "One", Kind: "wiki", Visibility: "private"}}, Articles: map[string]Article{"one/a.md": gappedArticle()}}
	if _, _, err := PutArticle(&db, "one", "a.md", "# New", 3, now); !errors.Is(err, ErrConflict) {
		t.Fatal("count accepted as revision", err)
	}
	rev, _, err := PutArticle(&db, "one", "a.md", "# New", 9, now)
	if err != nil || rev.Number != 10 {
		t.Fatal("expected revision 10", rev.Number, err)
	}
}
