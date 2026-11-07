// Package search implements full-text search over article pages and the
// ranking of its results.
package search

import (
	"bytes"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// Search remains lexical. These aliases cover common infrastructure terms;
// they do not infer intent or authorization from the user's request.
var aliases = map[string][]string{
	"контейнер": {"контейнер", "container", "lxc", "docker"},
	"container": {"контейнер", "container", "lxc", "docker"},
	"lxc":       {"lxc"},
	"docker":    {"docker"},
	"vps":       {"vps", "vds", "впс", "vsp"},
	"vds":       {"vps", "vds", "впс", "vsp"},
	"впс":       {"vps", "vds", "впс", "vsp"},
	"vsp":       {"vps", "vds", "впс", "vsp"},
}

// Hit identifies one matching article.
type Hit struct {
	SourceID string `json:"source_id"`
	Path     string `json:"path"`
	Title    string `json:"title"`
	Match    *Match `json:"match,omitempty"` // set by Page only
}

// Candidate is the visible revision text of one article, captured before
// ranking so concurrent imports cannot mix a title from one revision with
// another revision's body.
type Candidate struct {
	hit         Hit
	sourceTitle string
	text        string
	sha         string
}

// Candidates captures the visible revisions of db. Callers that share db with
// writers hold their read lock only for this call and rank afterwards.
func Candidates(db domain.Database, sourceID string, public bool, now time.Time) []Candidate {
	var candidates []Candidate
	for _, a := range db.Articles {
		if a.Deleted || (sourceID != "" && a.SourceID != sourceID) {
			continue
		}
		src, ok := db.Sources[a.SourceID]
		if !ok || (public && !domain.SourcePublicReadable(db, src, now)) {
			continue
		}
		rev, _ := domain.LastRevision(a)
		if public && src.ConnectionID != "" {
			if a.Published == nil {
				continue
			}
			rev = *a.Published
		}
		candidates = append(candidates, Candidate{hit: Hit{SourceID: a.SourceID, Path: a.Path, Title: domain.Title(rev.Text, a.Path)}, sourceTitle: src.Title, text: rev.Text, sha: rev.SHA})
	}
	return candidates
}

// Rank scores the candidates and returns the matches, best first, with ties in
// source/path order.
func Rank(candidates []Candidate, query string, cache *Cache) []Hit {
	matches := rankCached(candidates, query, cache)
	out := make([]Hit, 0, len(matches))
	for _, c := range matches {
		out = append(out, c.hit)
	}
	return out
}

// Page ranks the candidates and returns one page of matches with the best
// matching line of each, and the total number of matches. Only the page's
// articles are scanned for lines.
func Page(candidates []Candidate, query string, offset, limit int, cache *Cache) ([]Hit, int) {
	matches := rankCached(candidates, query, cache)
	start := min(offset, len(matches))
	end := start + min(limit, len(matches)-start)
	terms := Terms(query)
	out := make([]Hit, 0, end-start)
	for _, c := range matches[start:end] {
		hit := c.hit
		if hit.Match = FindMatch(c.text, query, terms); hit.Match != nil {
			hit.Match.SHA = c.sha
		}
		out = append(out, hit)
	}
	return out, len(matches)
}

// rankCached scores the candidates, taking lowercased bodies from cache; a
// nil cache lowercases every body for this query only.
func rankCached(candidates []Candidate, query string, cache *Cache) []Candidate {
	type ranked struct {
		candidate Candidate
		score     int
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].hit.SourceID+"/"+candidates[i].hit.Path < candidates[j].hit.SourceID+"/"+candidates[j].hit.Path
	})
	var matches []ranked
	terms := Terms(query)
	var scratch []byte
	for _, c := range candidates {
		var score int
		if cache != nil {
			articlePath := c.hit.SourceID + "/" + c.hit.Path
			metadata := domain.Title(c.text, articlePath) + " " + articlePath + " " + c.sourceTitle
			score = scoreCached(query, metadata, cache.lowered(c.sha, c.text), terms)
		} else {
			score = articleWithScratch(query, c.hit.SourceID+"/"+c.hit.Path, c.sourceTitle, c.text, terms, &scratch)
		}
		if score == 0 {
			continue
		}
		matches = append(matches, ranked{c, score})
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].score > matches[j].score })
	out := make([]Candidate, 0, len(matches))
	for _, item := range matches {
		out = append(out, item.candidate)
	}
	return out
}

// Find ranks the articles of a database snapshot visible to the caller.
func Find(db domain.Database, query, sourceID string, public bool, now time.Time) []Hit {
	return Rank(Candidates(db, sourceID, public, now), query, nil)
}

// Article scores the visible text of one article.
func Article(query, articlePath, sourceTitle, text string, terms [][]string) int {
	var scratch []byte
	return articleWithScratch(query, articlePath, sourceTitle, text, terms, &scratch)
}

func articleWithScratch(query, articlePath, sourceTitle, text string, terms [][]string, scratch *[]byte) int {
	return scoreWithScratch(query, domain.Title(text, articlePath)+" "+articlePath+" "+sourceTitle, text, terms, scratch)
}

var stopWords = map[string]bool{
	"как": true, "где": true, "мне": true, "мой": true, "моем": true, "моём": true, "создай": true, "создать": true,
	"сделай": true, "пожалуйста": true, "помоги": true, "для": true, "это": true, "на": true, "по": true, "из": true,
	"от": true, "за": true, "до": true, "во": true, "со": true,
	"the": true, "and": true, "for": true, "how": true, "create": true, "please": true, "with": true, "my": true, "on": true, "to": true,
}

// Terms splits a query into alias groups; every group is a set of alternatives.
func Terms(query string) [][]string {
	words := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	seen := map[string]bool{}
	result := [][]string{}
	for _, word := range words {
		if len([]rune(word)) < 2 || stopWords[word] {
			continue
		}
		if strings.HasPrefix(word, "контейнер") {
			word = "контейнер"
		}
		alternatives := aliases[word]
		if len(alternatives) == 0 {
			alternatives = []string{word}
		}
		key := alternatives[0]
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, alternatives)
		if len(result) == 16 {
			break
		}
	}
	return result
}

// An exact phrase always matches. For multiple meaningful words, require two
// distinct matches and rank title/path hits above body hits.
func score(query, metadata, body string, terms [][]string) int {
	var scratch []byte
	return scoreWithScratch(query, metadata, body, terms, &scratch)
}

func scoreWithScratch(query, metadata, body string, terms [][]string, scratch *[]byte) int {
	query = strings.ToLower(query)
	// A verbatim ASCII match is already case-insensitively equal. Check it
	// in a bounded prefix before allocating lowercased document copies. The
	// bound limits extra scanning when the exact phrase is absent.
	ascii := true
	for i := 0; i < len(query); i++ {
		if query[i] >= 0x80 {
			ascii = false
			break
		}
	}
	prefix := body
	if len(prefix) > 8192 {
		prefix = prefix[:8192]
	}
	if ascii && (strings.Contains(metadata, query) || strings.Contains(prefix, query)) {
		return 1000
	}
	metadata = strings.ToLower(metadata)
	// Reuse one request-local buffer. Unicode case mapping follows Go's
	// strings.ToLower, including non-ASCII to ASCII mappings.
	asciiBody := true
	for i := 0; i < len(body); i++ {
		if body[i] >= 0x80 {
			asciiBody = false
			break
		}
	}
	var lowered []byte
	if asciiBody {
		if cap(*scratch) < len(body) {
			*scratch = make([]byte, len(body))
		}
		lowered = (*scratch)[:len(body)]
		for i := range body {
			c := body[i]
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			lowered[i] = c
		}
	} else {
		if cap(*scratch) < len(body) {
			*scratch = make([]byte, 0, len(body))
		}
		lowered = (*scratch)[:0]
		for _, r := range body {
			lowered = utf8.AppendRune(lowered, unicode.ToLower(r))
		}
		*scratch = lowered
	}
	return scoreLowered(query, metadata, func(word string) bool {
		return bytes.Contains(lowered, []byte(word))
	}, terms)
}

// scoreCached is scoreWithScratch over a body already lowercased by Cache.
func scoreCached(query, metadata, lowered string, terms [][]string) int {
	return scoreLowered(strings.ToLower(query), strings.ToLower(metadata), func(word string) bool {
		return strings.Contains(lowered, word)
	}, terms)
}

// scoreLowered scores a lowercased query and metadata against a body whose
// lowercased form containsBody searches.
func scoreLowered(query, metadata string, containsBody func(string) bool, terms [][]string) int {
	if strings.Contains(metadata, query) || containsBody(query) {
		return 1000
	}
	hits, score := 0, 0
	for _, group := range terms {
		inMetadata, inBody := false, false
		for _, word := range group {
			if strings.Contains(metadata, word) {
				inMetadata = true
			}
			if containsBody(word) {
				inBody = true
			}
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
