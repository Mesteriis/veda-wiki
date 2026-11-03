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
}

// Candidate is the visible revision text of one article, captured before
// ranking so concurrent imports cannot mix a title from one revision with
// another revision's body.
type Candidate struct {
	hit         Hit
	sourceTitle string
	original    string
	displayed   string
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
		rev := a.Versions[len(a.Versions)-1]
		if public && src.ConnectionID != "" {
			if a.Published == nil {
				continue
			}
			rev = *a.Published
		}
		original := rev.Text
		rev, _ = domain.DisplayedRevision(a, rev, public)
		candidates = append(candidates, Candidate{hit: Hit{SourceID: a.SourceID, Path: a.Path, Title: domain.Title(rev.Text, a.Path)}, sourceTitle: src.Title, original: original, displayed: rev.Text})
	}
	return candidates
}

// Rank scores the candidates and returns the matches, best first, with ties in
// source/path order.
func Rank(candidates []Candidate, query string) []Hit {
	type ranked struct {
		hit   Hit
		score int
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].hit.SourceID+"/"+candidates[i].hit.Path < candidates[j].hit.SourceID+"/"+candidates[j].hit.Path
	})
	var matches []ranked
	terms := Terms(query)
	var scratch []byte
	for _, c := range candidates {
		score := revisionsWithScratch(query, c.hit.SourceID+"/"+c.hit.Path, c.sourceTitle, c.original, c.displayed, terms, &scratch)
		if score == 0 {
			continue
		}
		matches = append(matches, ranked{c.hit, score})
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].score > matches[j].score })
	out := make([]Hit, 0, len(matches))
	for _, item := range matches {
		out = append(out, item.hit)
	}
	return out
}

// Find ranks the articles of a database snapshot visible to the caller.
func Find(db domain.Database, query, sourceID string, public bool, now time.Time) []Hit {
	return Rank(Candidates(db, sourceID, public, now), query)
}

// Score each text independently: words from different translations must not
// combine into a match that exists in neither version.
// Revisions scores the original and displayed texts of one article.
func Revisions(query, articlePath, sourceTitle, original, displayed string, terms [][]string) int {
	var scratch []byte
	return revisionsWithScratch(query, articlePath, sourceTitle, original, displayed, terms, &scratch)
}

func revisionsWithScratch(query, articlePath, sourceTitle, original, displayed string, terms [][]string, scratch *[]byte) int {
	originalScore := scoreWithScratch(query, domain.Title(original, articlePath)+" "+articlePath+" "+sourceTitle, original, terms, scratch)
	if original == displayed {
		return originalScore
	}
	displayedScore := scoreWithScratch(query, domain.Title(displayed, articlePath)+" "+articlePath+" "+sourceTitle, displayed, terms, scratch)
	return max(originalScore, displayedScore)
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
	containsBody := func(word string) bool {
		return bytes.Contains(lowered, []byte(word))
	}
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
