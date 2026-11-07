package search

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// The cached path must rank exactly like lowercasing per query.
func TestCachedRankingMatchesUncached(t *testing.T) {
	bodies := []string{
		"# Сервер\nКонтейнер LXC и Docker на PVE",
		"# VPS\nnginx proxy, ВПС в России",
		"# Mixed\nİstanbul ǅ ΣΑΣ \xff invalid byte, Proxmox",
		"plain ascii body about Proxmox backups",
		"# Пусто",
	}
	var candidates []Candidate
	for i, body := range bodies {
		path := fmt.Sprintf("p%d.md", i)
		candidates = append(candidates, Candidate{hit: Hit{SourceID: "s", Path: path}, sourceTitle: "Infra", text: body, sha: fmt.Sprintf("sha%d", i)})
	}
	cache := NewCache()
	for _, query := range []string{"контейнер", "lxc docker", "VPS", "впс россии", "proxmox", "istanbul", "σας", "infra", "p3.md", "nothing here", "ǆ"} {
		for round := 0; round < 2; round++ { // the second round reads the cache
			plain := Rank(append([]Candidate(nil), candidates...), query, nil)
			cached := Rank(append([]Candidate(nil), candidates...), query, cache)
			if !reflect.DeepEqual(plain, cached) {
				t.Fatalf("%q round %d:\n%v\n%v", query, round, plain, cached)
			}
		}
	}
	if len(cache.bodies) != len(bodies) {
		t.Fatalf("cache holds %d bodies", len(cache.bodies))
	}
}

func TestCacheStartsOverPastItsBound(t *testing.T) {
	cache := NewCache()
	big := strings.Repeat("a", cacheBytes/2+1)
	cache.lowered("one", big)
	cache.lowered("two", big)
	if len(cache.bodies) != 1 || cache.bodies["two"] == "" || cache.size != len(big) {
		t.Fatalf("entries %d size %d", len(cache.bodies), cache.size)
	}
	if got := cache.lowered("", "ABC"); got != "abc" || len(cache.bodies) != 1 {
		t.Fatal("an empty sha must not be cached")
	}
	var none *Cache
	if none.lowered("x", "Ж") != "ж" {
		t.Fatal("nil cache")
	}
}
