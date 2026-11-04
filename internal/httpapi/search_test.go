package httpapi

import (
	"encoding/json"
	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
	"github.com/Mesteriis/veda-wiki/internal/search"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

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
			s.mutate(t, func(db *domain.Database) {
				a := db.Articles["src/guide.md"]
				if i%2 == 0 {
					a.Versions[0].Text = "# beta\n\nbeta"
				} else {
					a.Versions[0].Text = "# alpha\n\nalpha"
				}
				db.Articles["src/guide.md"] = a
			})
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
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "# Published guide\npublic keyword"})
	connectFixture(t, s, f, false)
	s.mutate(t, func(db *domain.Database) {
		a := db.Articles["handbook/guide.md"]
		text := "# Confidential local\nprivate keyword"
		a.Versions = append(a.Versions, domain.Revision{Number: 2, Text: text, SHA: domain.Digest([]byte(text))})
		db.Articles["handbook/guide.md"] = a
	})
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

func TestSearchIncludesVisibleOriginal(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"docs/guide.md": "# Deployment guide\n\nInstall the service."})
	connectFixture(t, s, f, false)
	a, err := s.article("handbook", "docs/guide.md", false)
	if err != nil {
		t.Fatal(err)
	}
	if hits := s.findArticles("Deployment", "", true); len(hits) != 1 || hits[0].Title != "Deployment guide" {
		t.Fatalf("search missed visible document: %+v", hits)
	}
	w := Wiki{Store: s, Public: true}
	response := httptest.NewRecorder()
	w.ServeHTTP(response, httptest.NewRequest("GET", "/api/state?q="+url.QueryEscape("Deployment"), nil))
	var result struct {
		View struct {
			Pages []domain.Page `json:"Pages"`
		} `json:"view"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || len(result.View.Pages) != 1 {
		t.Fatalf("browser search missed visible document: %d", response.Code)
	}
	s.mutate(t, func(db *domain.Database) {
		a = db.Articles["handbook/docs/guide.md"]
		local := "# Hidden local draft\n\nSecret term."
		a.Versions = append(a.Versions, domain.Revision{Number: 2, Text: local, SHA: domain.Digest([]byte(local))})
		db.Articles["handbook/docs/guide.md"] = a
	})
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
	key, err := s.auth().CreateAccessKey("reader", []string{"open"}, time.Now().Add(time.Hour))
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
				Pages []domain.Page `json:"Pages"`
			} `json:"view"`
		}
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || len(result.View.Pages) != 1 || result.View.Pages[0].Path != "open/docs/guide.md" || strings.Contains(response.Body.String(), "private") {
			t.Fatal("search leaked or omitted permitted result", response.Code)
		}
	}
}

func TestSearchIncludesVisibleSourceTitle(t *testing.T) {
	s := testStore(t)
	for _, src := range []domain.Source{
		{ID: "open", Title: "Example Handbook", Kind: "wiki", Visibility: "public"},
		{ID: "private", Title: "Example Handbook", Kind: "wiki", Visibility: "private"},
	} {
		if err := s.addSource(src); err != nil {
			t.Fatal(err)
		}
		if _, err := s.put(src.ID, "guide.md", "# Setup\n\nLXC installation", 0); err != nil {
			t.Fatal(err)
		}
	}
	key, err := s.auth().CreateAccessKey("reader", []string{"open"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	w := Wiki{Store: s, Token: "owner"}
	for _, auth := range []string{"", key} {
		for _, route := range []string{"/api/v1/search?q=example+handbook", "/api/state?q=example+handbook"} {
			r := httptest.NewRequest("GET", route, nil)
			if auth != "" {
				r.Header.Set("Authorization", "Bearer "+auth)
			}
			out := httptest.NewRecorder()
			w.ServeHTTP(out, r)
			if out.Code != 200 || !strings.Contains(out.Body.String(), "guide.md") || strings.Contains(out.Body.String(), "private") {
				t.Fatal("source title search failed or leaked restricted source", route, out.Code)
			}
		}
	}
	if hits := s.findArticles("example handbook", "", false); len(hits) != 2 {
		t.Fatal("private search missed a source title", len(hits))
	}
}

func TestCompactSearchPaginationAndScope(t *testing.T) {
	s := testStore(t)
	source(t, s, "open", "public")
	source(t, s, "private", "private")
	s.put("open", "docs/a.md", "# LXC alpha\nVPS", 0)
	s.put("open", "docs/b.md", "# LXC beta\nVPS", 0)
	s.put("private", "docs/hidden.md", "# LXC secret\nVPS", 0)
	key, err := s.auth().CreateAccessKey("agent", []string{"open"}, time.Now().Add(time.Hour))
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
			Results []search.Hit `json:"results"`
			Total   int          `json:"total"`
			Offset  int          `json:"offset"`
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
