package main

import (
	"strings"
	"testing"
)

func TestPartialPublicImportHidesStaleSnapshotAndRecovers(t *testing.T) {
	s := testStore(t)
	f := newFakeGitHub(t, map[string]string{"docs/guide.md": "# Original", "docs/image.png": string(smallPNG(t))})
	m := connectFixture(t, s, f, false)
	if _, err := s.page("handbook/docs/guide.md", true); err != nil {
		t.Fatal("initial public article missing", err)
	}
	// Preserve a local unapproved revision so a remote deletion creates a conflict.
	s.mu.Lock()
	a := s.db.Articles["handbook/docs/guide.md"]
	local := "# Private local draft"
	a.Versions = append(a.Versions, Revision{Number: 2, Text: local, SHA: digest([]byte(local))})
	a.PublishedLocal = false
	s.db.Articles["handbook/docs/guide.md"] = a
	s.mu.Unlock()
	f.advance(map[string]string{"docs/other.md": "# Remaining", "docs/image.png": string(smallPNG(t)), "docs/bad.png": "not an image"})
	syncFixtureSource(t, m, "handbook")
	if !s.db.Sources["handbook"].ImportPartial {
		t.Fatal("fixture did not produce a partial import")
	}
	s, err := openStore(s.dir)
	if err != nil {
		t.Fatal("cannot reopen partial snapshot", err)
	}
	if len(s.sources(true)) != 0 || len(s.snapshot(true).Pages) != 0 || len(s.findArticles("Original", "", true)) != 0 {
		t.Fatal("partial public snapshot was enumerable")
	}
	if _, err = s.page("handbook/docs/guide.md", true); err != ErrNotFound {
		t.Fatal("partial snapshot exposed old page", err)
	}
	if _, err = s.article("handbook", "docs/guide.md", true); err != ErrNotFound {
		t.Fatal("partial snapshot exposed old article", err)
	}
	if _, err = s.agentContext("handbook", "docs/guide.md", true); err != ErrNotFound {
		t.Fatal("partial snapshot exposed context", err)
	}
	if r := request(Wiki{Store: s, Public: true}, "GET", "/media/handbook/docs/image.png", "", false); r.Code != 404 {
		t.Fatal("partial snapshot exposed an image", r.Code)
	}
	if page, err := s.page("handbook/docs/guide.md", false); err != nil || page.Text != local {
		t.Fatal("local draft not retained", err)
	}
	f.advance(map[string]string{"docs/other.md": "# Remaining"})
	m = newSyncManager(s, f.client())
	syncFixtureSource(t, m, "handbook")
	if s.db.Sources["handbook"].ImportPartial || len(s.snapshot(true).Pages) != 1 {
		t.Fatal("complete snapshot did not restore public access")
	}
	if _, err = s.page("handbook/docs/guide.md", true); err != ErrNotFound {
		t.Fatal("deleted upstream article remained public", err)
	}
	if page, err := s.page("handbook/docs/guide.md", false); err != nil || page.Text != local || len(s.db.Articles["handbook/docs/guide.md"].Conflicts) == 0 {
		t.Fatal("deleted upstream article lost local draft or conflict", err)
	}
}

func TestPartialLocalPublicImportHidesStaleSnapshotAndRecovers(t *testing.T) {
	s := testStore(t)
	b := testBatch()
	b.Visibility = "public"
	b.Files = map[string]string{"AGENTS.md": "# Routing", "docs/old.md": "# Old public document"}
	b.Assets = map[string][]byte{"images/old.png": smallPNG(t)}
	if _, err := s.importBatch(b); err != nil {
		t.Fatal(err)
	}
	if _, err := s.page("handbook/docs/old.md", true); err != nil {
		t.Fatal("initial public article missing", err)
	}
	b = applyAgain(s, b)
	b.Files = map[string]string{"docs/new.md": "# New document"}
	b.Assets = map[string][]byte{}
	b.Withheld = 1
	if _, err := s.importBatch(b); err != nil {
		t.Fatal(err)
	}
	if !s.db.Sources["handbook"].ImportPartial {
		t.Fatal("fixture must be partial")
	}
	s, err := openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.sources(true)) != 0 || len(s.snapshot(true).Pages) != 0 || len(s.findArticles("Old public", "", true)) != 0 {
		t.Fatal("partial local public import remains enumerable")
	}
	for _, name := range []string{"docs/old.md", "docs/new.md"} {
		if _, err = s.page("handbook/"+name, true); err != ErrNotFound {
			t.Fatal("partial local page exposed", name)
		}
		if _, err = s.article("handbook", name, true); err != ErrNotFound {
			t.Fatal("partial local article exposed", name)
		}
	}
	if _, err = s.agentContext("handbook", "docs/old.md", true); err != ErrNotFound {
		t.Fatal("partial local context exposed")
	}
	w := Wiki{Store: s, Public: true}
	for _, route := range []string{"/api/state", "/api/v1/sources", "/api/v1/search?q=Old", "/api/v1/articles"} {
		got := request(w, "GET", route, "", false)
		if got.Code != 200 || strings.Contains(got.Body.String(), "handbook") || strings.Contains(got.Body.String(), "Old public") {
			t.Fatal("partial local source exposed on public route", route, got.Code)
		}
	}
	if got := request(w, "GET", "/api/v1/articles?source_id=handbook&path=docs%2Fold.md", "", false); got.Code != 404 {
		t.Fatal("partial local article exposed by API", got.Code)
	}
	if got := request(w, "GET", "/media/handbook/images/old.png", "", false); got.Code != 404 {
		t.Fatal("partial local image exposed", got.Code)
	}
	if _, err = s.page("handbook/docs/old.md", false); err != nil {
		t.Fatal("private reader lost old article", err)
	}
	b = applyAgain(s, b)
	b.Withheld = 0
	b.Commit = strings.Repeat("c", 40)
	if _, err = s.importBatch(b); err != nil {
		t.Fatal(err)
	}
	if s.db.Sources["handbook"].ImportPartial {
		t.Fatal("complete import remained partial")
	}
	if _, err = s.page("handbook/docs/old.md", true); err != ErrNotFound {
		t.Fatal("removed article remains public", err)
	}
	if _, err = s.page("handbook/docs/new.md", true); err != nil {
		t.Fatal("public access did not recover", err)
	}
}
