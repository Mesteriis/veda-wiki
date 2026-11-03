package main

import (
	"encoding/json"
	"math/rand"
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
	if score := searchScore("создай контейнер на моём VPS", "example-vps/docs/setup.md", "Deploy an LXC guest", terms); score == 0 {
		t.Fatal("alias and source match missing")
	}
	qTypo := "создай контейнер на моем VSP"
	if score := searchScore(qTypo, "example-vps/docs/setup.md", "Deploy an LXC guest", searchTerms(qTypo)); score == 0 {
		t.Fatal("VSP alias did not find VPS documentation")
	}
	if score := searchScore(qTypo, "example-vps/docs/docker.md", "Docker service", searchTerms(qTypo)); score == 0 {
		t.Fatal("container request did not find Docker documentation")
	}
	if score := searchScore("lxc vps", "example-vps/docs/docker.md", "Docker service", searchTerms("lxc vps")); score != 0 {
		t.Fatal("specific LXC request matched Docker-only documentation")
	}
	if score := searchScore("создай контейнер на моём VPS", "example-vps/docs/backup.md", "Backup instructions", terms); score != 0 {
		t.Fatal("single token matched a multiword query")
	}
	q := "container VPS"
	ts := searchTerms(q)
	exact := searchScore(q, "container VPS setup", "", ts)
	partial := searchScore(q, "example-vps/setup.md", "LXC setup", ts)
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

func TestSearchIncludesVisibleSourceTitle(t *testing.T) {
	s := testStore(t)
	for _, src := range []Source{
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
	key, err := s.createAccessKey("reader", []string{"open"}, time.Now().Add(time.Hour))
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

func TestExactPhraseFastPathPreservesSearchRanking(t *testing.T) {
	for _, tc := range []struct {
		query, metadata, body string
		want                  int
	}{
		{"container vps", "path", "Mixed heading\ncontainer vps\nmore", 1000},
		{"CONTAINER VPS", "path", "Mixed heading\ncontainer vps\nmore", 1000},
		{"container vps", "path", "Mixed heading\nCONTAINER VPS\nmore", 1000},
		{"контейнер VPS", "path", "Контейнер VPS", 1000},
		{"k", "path", "K", 1000},
		{"container vps", "path", strings.Repeat("X", 8190) + "container vps", 1000},
		{"lxc vps", "vps/docker.md", "Docker service", 0},
	} {
		if got := searchScore(tc.query, tc.metadata, tc.body, searchTerms(tc.query)); got != tc.want {
			t.Fatalf("query %q got %d, want %d", tc.query, got, tc.want)
		}
	}
}

// Compare the request-local scratch path with the original strings.ToLower
// semantics, including Unicode fallback and successive documents of different sizes.
func TestSearchScratchMatchesOriginalScoring(t *testing.T) {
	reference := func(query, metadata, body string, terms [][]string) int {
		query, metadata, body = strings.ToLower(query), strings.ToLower(metadata), strings.ToLower(body)
		if strings.Contains(metadata, query) || strings.Contains(body, query) {
			return 1000
		}
		hits, score := 0, 0
		for _, group := range terms {
			inMetadata, inBody := false, false
			for _, word := range group {
				inMetadata = inMetadata || strings.Contains(metadata, word)
				inBody = inBody || strings.Contains(body, word)
			}
			if inMetadata || inBody {
				hits++
				if inMetadata {
					score += 3
				} else {
					score++
				}
			}
		}
		required := 1
		if len(terms) > 1 {
			required = 2
		}
		if len(terms) == 0 || hits < required {
			return 0
		}
		return score
	}
	rng := rand.New(rand.NewSource(42))
	words := []string{"Container", "vPs", "LXC", "docker", "other", "КОНТЕЙНЕР", "K", "Ⱥ", "İ", "\xff", ""}
	queries := []string{"container vps", "LXC", "контейнер", "K", "docker vps", "other", "создай", "K", "Ⱥ", "İ", "\xff"}
	var scratch []byte
	for i := 0; i < 1000; i++ {
		var body strings.Builder
		for j := 0; j < rng.Intn(30); j++ {
			body.WriteString(words[rng.Intn(len(words))])
			body.WriteByte(' ')
		}
		q, meta := queries[rng.Intn(len(queries))], words[rng.Intn(len(words))]
		terms := searchTerms(q)
		got := searchScoreWithScratch(q, meta, body.String(), terms, &scratch)
		want := reference(q, meta, body.String(), terms)
		if got != want {
			t.Fatalf("case %d query %q metadata %q: got %d want %d", i, q, meta, got, want)
		}
	}
}
