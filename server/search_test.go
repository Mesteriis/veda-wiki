package main

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
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
	if score := searchScore(qTypo, "vps_ru/docs/docker.md", "Docker service", searchTerms(qTypo)); score == 0 {
		t.Fatal("container request did not find Docker documentation")
	}
	if score := searchScore("lxc vps", "vps_ru/docs/docker.md", "Docker service", searchTerms("lxc vps")); score != 0 {
		t.Fatal("specific LXC request matched Docker-only documentation")
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

func TestSearchUsesOneRevisionDuringConcurrentUpdates(t *testing.T) {
	s := testStore(t)
	source(t, s, "src", "private")
	if _, err := s.put("src", "guide.md", "# alpha\n\nalpha", 0); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			s.mu.Lock()
			a := s.db.Articles["src/guide.md"]
			if i%2 == 0 {
				a.Versions[0].Text = "# beta\n\nbeta"
			} else {
				a.Versions[0].Text = "# alpha\n\nalpha"
			}
			s.db.Articles["src/guide.md"] = a
			s.mu.Unlock()
		}
	}()
	for i := 0; i < 1000; i++ {
		if len(s.findArticles("alpha beta", "src", false)) != 0 {
			wg.Wait()
			t.Fatal("search matched terms from different revisions")
		}
	}
	wg.Wait()
}

func TestPublicSearchUsesPublishedRevision(t *testing.T) {
	s := testStore(t)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "# Published guide\npublic keyword"})
	connectFixture(t, s, f, false)
	s.mu.Lock()
	a := s.db.Articles["handbook/guide.md"]
	text := "# Confidential local\nprivate keyword"
	a.Versions = append(a.Versions, Revision{Number: 2, Text: text, SHA: digest([]byte(text))})
	s.db.Articles["handbook/guide.md"] = a
	s.mu.Unlock()
	if hits := s.findArticles("confidential", "", true); len(hits) != 0 {
		t.Fatal("public search leaked local revision")
	}
	if hits := s.findArticles("published", "", true); len(hits) != 1 || hits[0].Title != "Published guide" {
		t.Fatal("published revision missing")
	}
	if hits := s.findArticles("confidential", "", false); len(hits) != 1 || hits[0].Title != "Confidential local" {
		t.Fatal("private revision missing")
	}
}

func TestSearchIncludesVisibleOriginalAndTranslation(t *testing.T) {
	s := testStore(t)
	f := newFakeGitHub(t, map[string]string{"docs/guide.md": "# Deployment guide\n\nInstall the service."})
	connectFixture(t, s, f, false)
	a, err := s.article("handbook", "docs/guide.md", false)
	if err != nil {
		t.Fatal(err)
	}
	original := a.Versions[0]
	_, err = s.submitTranslation(translationRequest{SourceID: "handbook", Path: "docs/guide.md", ExpectedRevision: 1, OriginalSHA: original.SHA, Text: "# Руководство\n\nУстановите службу.", ConfirmPublication: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"Deployment", "Руководство"} {
		if hits := s.findArticles(q, "", true); len(hits) != 1 || hits[0].Title != "Руководство" {
			t.Fatalf("search %q missed visible document: %+v", q, hits)
		}
		w := Wiki{Store: s, Public: true}
		response := httptest.NewRecorder()
		request := httptest.NewRequest("GET", "/api/state?q="+url.QueryEscape(q), nil)
		w.ServeHTTP(response, request)
		var result struct {
			View struct {
				Pages []Page `json:"Pages"`
			} `json:"view"`
		}
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || len(result.View.Pages) != 1 {
			t.Fatalf("browser search %q missed visible document: %d", q, response.Code)
		}
	}
	if hits := s.findArticles("Deployment Установите", "", true); len(hits) != 0 {
		t.Fatal("search combined words from different texts")
	}
	s.mu.Lock()
	a = s.db.Articles["handbook/docs/guide.md"]
	a.Translation.PublicApproved = false
	s.db.Articles["handbook/docs/guide.md"] = a
	s.mu.Unlock()
	if hits := s.findArticles("Руководство", "", true); len(hits) != 0 {
		t.Fatal("public search exposed unapproved translation")
	}
	if hits := s.findArticles("Руководство", "", false); len(hits) != 1 {
		t.Fatal("private search missed private translation")
	}
	response := httptest.NewRecorder()
	Wiki{Store: s, Public: true}.ServeHTTP(response, httptest.NewRequest("GET", "/api/state?q="+url.QueryEscape("Руководство"), nil))
	var result struct {
		View struct {
			Pages []Page `json:"Pages"`
		} `json:"view"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || len(result.View.Pages) != 0 {
		t.Fatal("browser search exposed unapproved translation")
	}
	s.mu.Lock()
	a = s.db.Articles["handbook/docs/guide.md"]
	local := "# Hidden local draft\n\nSecret term."
	a.Versions = append(a.Versions, Revision{Number: 2, Text: local, SHA: digest([]byte(local))})
	s.db.Articles["handbook/docs/guide.md"] = a
	s.mu.Unlock()
	if hits := s.findArticles("Hidden", "", true); len(hits) != 0 {
		t.Fatal("public search exposed unpublished original")
	}
	if hits := s.findArticles("Deployment", "", true); len(hits) != 1 {
		t.Fatal("published original disappeared after private edit")
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

func TestCompactSearchPaginationAndScope(t *testing.T) {
	s := testStore(t)
	source(t, s, "open", "public")
	source(t, s, "private", "private")
	s.put("open", "docs/a.md", "# LXC alpha\nVPS", 0)
	s.put("open", "docs/b.md", "# LXC beta\nVPS", 0)
	s.put("private", "docs/hidden.md", "# LXC secret\nVPS", 0)
	key, err := s.createAccessKey("agent", []string{"open"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	w := Wiki{Store: s, Token: "owner"}
	call := func(path, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		out := httptest.NewRecorder()
		w.ServeHTTP(out, r)
		return out
	}
	for _, token := range []string{"", key} {
		r := call("/api/v1/search?q=lxc+vps&limit=1&offset=1", token)
		var result struct {
			Results []searchHit `json:"results"`
			Total   int         `json:"total"`
			Offset  int         `json:"offset"`
		}
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &result) != nil || result.Total != 2 || result.Offset != 1 || len(result.Results) != 1 || result.Results[0].SourceID != "open" || strings.Contains(r.Body.String(), "private") {
			t.Fatal("invalid page or scope", r.Code)
		}
		filtered := call("/api/v1/search?q=lxc+vps&source_id=private", token)
		if filtered.Code != 200 || !strings.Contains(filtered.Body.String(), `"total":0`) {
			t.Fatal("restricted source was enumerable", filtered.Code)
		}
	}
	if call("/api/v1/search?q=", key).Code != 400 || call("/api/v1/search?q=lxc&limit=101", key).Code != 400 {
		t.Fatal("invalid query accepted")
	}
}
