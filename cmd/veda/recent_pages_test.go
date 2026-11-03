package main

import (
	"encoding/json"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
	"testing"
)

func TestRecentCardsUseVisibleRevisionTimes(t *testing.T) {
	s := testStore(t)
	source(t, s, "public", "public")
	source(t, s, "private", "private")
	for _, item := range []struct{ source, path, when string }{
		{"public", "a.md", "2026-01-01T10:00:00Z"},
		{"public", "b.md", "2026-02-01T10:00:00Z"},
		{"public", "c.md", "2026-02-01T10:00:00Z"},
		{"private", "secret.md", "2026-03-01T10:00:00Z"},
	} {
		if _, err := s.put(item.source, item.path, "# "+item.path, 0); err != nil {
			t.Fatal(err)
		}
		s.mu.Lock()
		a := s.db.Articles[item.source+"/"+item.path]
		a.Versions[0].Created = item.when
		s.db.Articles[item.source+"/"+item.path] = a
		s.mu.Unlock()
	}
	read := func(public bool) []Card {
		t.Helper()
		w := Wiki{Store: s, Token: "test-owner", Public: public}
		r := request(w, "GET", "/api/state", "", !public)
		var response struct {
			View struct{ Cards []Card } `json:"view"`
		}
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &response) != nil {
			t.Fatal("state unavailable", r.Code)
		}
		return response.View.Cards
	}
	private := read(false)
	if len(private) != 4 || private[0].Page.Path != "private/secret.md" || private[1].Page.Path != "public/b.md" || private[2].Page.Path != "public/c.md" || private[3].Page.Path != "public/a.md" {
		t.Fatal("incorrect recent ordering")
	}
	public := read(true)
	if len(public) != 3 || public[0].Page.Path != "public/b.md" || public[0].Page.Updated != "2026-02-01T10:00:00Z" {
		t.Fatal("public recent view leaked or reordered data")
	}
}

func TestPublicRecentTimeUsesPublishedRevision(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "# Published"})
	connectFixture(t, s, f, false)
	s.mu.Lock()
	a := s.db.Articles["handbook/guide.md"]
	if a.Published == nil {
		s.mu.Unlock()
		t.Fatal("published fixture unavailable")
	}
	a.Published.Created = "2026-01-01T10:00:00Z"
	text := "# Private local revision"
	a.Versions = append(a.Versions, Revision{Number: 2, Text: text, SHA: digest([]byte(text)), Created: "2026-03-01T10:00:00Z"})
	s.db.Articles["handbook/guide.md"] = a
	s.mu.Unlock()
	private := s.snapshot(false)
	public := s.snapshot(true)
	if len(private.Pages) != 1 || private.Pages[0].Updated != "2026-03-01T10:00:00Z" {
		t.Fatal("private revision time missing")
	}
	if len(public.Pages) != 1 || public.Pages[0].Updated != "2026-01-01T10:00:00Z" || public.Pages[0].Title != "Published" {
		t.Fatal("public metadata revealed private revision")
	}
}
