package search

import (
	"encoding/json"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

func TestSearchTermsAndRanking(t *testing.T) {
	terms := Terms("создай контейнер на моём VPS")
	if len(terms) != 2 {
		t.Fatal("unexpected tokens", terms)
	}
	if score := score("создай контейнер на моём VPS", "example-vps/docs/setup.md", "Deploy an LXC guest", terms); score == 0 {
		t.Fatal("alias and source match missing")
	}
	qTypo := "создай контейнер на моем VSP"
	if score := score(qTypo, "example-vps/docs/setup.md", "Deploy an LXC guest", Terms(qTypo)); score == 0 {
		t.Fatal("VSP alias did not find VPS documentation")
	}
	if score := score(qTypo, "example-vps/docs/docker.md", "Docker service", Terms(qTypo)); score == 0 {
		t.Fatal("container request did not find Docker documentation")
	}
	if score := score("lxc vps", "example-vps/docs/docker.md", "Docker service", Terms("lxc vps")); score != 0 {
		t.Fatal("specific LXC request matched Docker-only documentation")
	}
	if score := score("создай контейнер на моём VPS", "example-vps/docs/backup.md", "Backup instructions", terms); score != 0 {
		t.Fatal("single token matched a multiword query")
	}
	q := "container VPS"
	ts := Terms(q)
	exact := score(q, "container VPS setup", "", ts)
	partial := score(q, "example-vps/setup.md", "LXC setup", ts)
	if exact <= partial || partial == 0 {
		t.Fatal("unexpected ranking", exact, partial)
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
		if got := score(tc.query, tc.metadata, tc.body, Terms(tc.query)); got != tc.want {
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
		terms := Terms(q)
		got := scoreWithScratch(q, meta, body.String(), terms, &scratch)
		want := reference(q, meta, body.String(), terms)
		if got != want {
			t.Fatalf("case %d query %q metadata %q: got %d want %d", i, q, meta, got, want)
		}
	}
}

func TestFindHonoursPublicationAndSourceFilter(t *testing.T) {
	text := func(s string) []domain.Revision { return []domain.Revision{{Number: 1, Text: s}} }
	db := domain.Database{
		Sources: map[string]domain.Source{
			"a": {ID: "a", Title: "A", Kind: "wiki", Visibility: "private"},
			"b": {ID: "b", Title: "B", Kind: "wiki", Visibility: "private"},
		},
		Articles: map[string]domain.Article{
			"a/x.md": {SourceID: "a", Path: "x.md", Versions: text("# Alpha guide\nkeyword")},
			"b/y.md": {SourceID: "b", Path: "y.md", Versions: text("# Beta guide\nkeyword")},
			"b/z.md": {SourceID: "b", Path: "z.md", Versions: text("# Gone\nkeyword"), Deleted: true},
		},
	}
	now := time.Now()
	hits := Find(db, "keyword", "", false, now)
	if len(hits) != 2 || hits[0] != (Hit{SourceID: "a", Path: "x.md", Title: "Alpha guide"}) {
		t.Fatalf("%+v", hits)
	}
	if hits := Find(db, "keyword", "b", false, now); len(hits) != 1 || hits[0].Path != "y.md" {
		t.Fatalf("%+v", hits)
	}
	if hits := Find(db, "keyword", "", true, now); len(hits) != 0 {
		t.Fatalf("private source leaked to public search: %+v", hits)
	}
	b, _ := json.Marshal(Hit{SourceID: "s", Path: "p", Title: "t"})
	if string(b) != `{"source_id":"s","path":"p","title":"t"}` {
		t.Fatal(string(b))
	}
}
