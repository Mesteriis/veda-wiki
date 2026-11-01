package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSearchTermsAndRanking(t *testing.T) {
	terms := searchTerms("создай контейнер на моём VPS")
	if len(terms) != 2 {
		t.Fatal("unexpected tokens", terms)
	}
	if score := searchScore("создай контейнер на моём VPS", "vps_ru/docs/setup.md", "Deploy an LXC guest", terms); score == 0 {
		t.Fatal("alias and source match missing")
	}
	qTypo := "создай контейнер на моем VSP"
	if score := searchScore(qTypo, "vps_ru/docs/setup.md", "Deploy an LXC guest", searchTerms(qTypo)); score == 0 {
		t.Fatal("VSP alias did not find VPS documentation")
	}
	if score := searchScore("создай контейнер на моём VPS", "vps_ru/docs/backup.md", "Backup instructions", terms); score != 0 {
		t.Fatal("single token matched a multiword query")
	}
	q := "container VPS"
	ts := searchTerms(q)
	exact := searchScore(q, "container VPS setup", "", ts)
	partial := searchScore(q, "vps_ru/setup.md", "LXC setup", ts)
	if exact <= partial || partial == 0 {
		t.Fatal("unexpected ranking", exact, partial)
	}
}

func TestSearchKeepsPublicAndScopedSourcesSeparate(t *testing.T) {
	s := testStore(t)
	source(t, s, "open", "public")
	source(t, s, "private", "private")
	s.put("open", "docs/guide.md", "# LXC guide\nVPS installation", 0)
	s.put("private", "docs/secret.md", "# LXC private canary\nVPS installation", 0)
	key, err := s.createAccessKey("reader", []string{"open"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	w := Wiki{Store: s, Token: "owner"}
	for _, auth := range []string{"", key} {
		r := httptest.NewRequest("GET", "/api/state?q=container+VPS", nil)
		if auth != "" {
			r.Header.Set("Authorization", "Bearer "+auth)
		}
		response := httptest.NewRecorder()
		w.ServeHTTP(response, r)
		var result struct {
			View struct {
				Pages []Page `json:"Pages"`
			} `json:"view"`
		}
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || len(result.View.Pages) != 1 || result.View.Pages[0].Path != "open/docs/guide.md" || strings.Contains(response.Body.String(), "private") {
			t.Fatal("search leaked or omitted permitted result", response.Code)
		}
	}
}
