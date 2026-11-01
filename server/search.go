package main

import (
	"sort"
	"strings"
	"unicode"
)

// Search remains lexical. These aliases cover common infrastructure terms;
// they do not infer intent or authorization from the user's request.
var searchAliases = map[string][]string{
	"контейнер": {"контейнер", "container", "lxc", "docker"},
	"container": {"контейнер", "container", "lxc", "docker"},
	"lxc":       {"lxc"},
	"docker":    {"docker"},
	"vps":       {"vps", "vds", "впс", "vsp"},
	"vds":       {"vps", "vds", "впс", "vsp"},
	"впс":       {"vps", "vds", "впс", "vsp"},
	"vsp":       {"vps", "vds", "впс", "vsp"},
}

type searchHit struct {
	SourceID string `json:"source_id"`
	Path     string `json:"path"`
	Title    string `json:"title"`
}

// Only visible snapshots are considered; the caller paginates after filtering.
func (s *Store) findArticles(query, sourceID string, public bool) []searchHit {
	type ranked struct {
		hit   searchHit
		score int
	}
	var matches []ranked
	terms := searchTerms(query)
	for _, page := range s.snapshot(public).Pages {
		if sourceID != "" && page.Environment != sourceID {
			continue
		}
		full, err := s.page(page.Path, public)
		if err != nil {
			continue
		}
		score := searchScore(query, page.Title+" "+page.Path, full.Text, terms)
		if score == 0 {
			continue
		}
		prefix := page.Environment + "/"
		if !strings.HasPrefix(page.Path, prefix) {
			continue
		}
		matches = append(matches, ranked{searchHit{SourceID: page.Environment, Path: strings.TrimPrefix(page.Path, prefix), Title: page.Title}, score})
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].score > matches[j].score })
	out := make([]searchHit, 0, len(matches))
	for _, item := range matches {
		out = append(out, item.hit)
	}
	return out
}

var searchStopWords = map[string]bool{
	"как": true, "где": true, "мне": true, "мой": true, "моем": true, "моём": true, "создай": true, "создать": true, "сделай": true, "пожалуйста": true, "помоги": true, "для": true, "это": true, "на": true, "по": true, "из": true, "от": true, "за": true, "до": true, "во": true, "со": true,
	"the": true, "and": true, "for": true, "how": true, "create": true, "please": true, "with": true, "my": true, "on": true, "to": true,
}

func searchTerms(query string) [][]string {
	words := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	seen := map[string]bool{}
	result := [][]string{}
	for _, word := range words {
		if len([]rune(word)) < 2 || searchStopWords[word] {
			continue
		}
		if strings.HasPrefix(word, "контейнер") {
			word = "контейнер"
		}
		alternatives := searchAliases[word]
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
func searchScore(query, metadata, body string, terms [][]string) int {
	query = strings.ToLower(query)
	metadata = strings.ToLower(metadata)
	body = strings.ToLower(body)
	if strings.Contains(metadata, query) || strings.Contains(body, query) {
		return 1000
	}
	hits, score := 0, 0
	for _, group := range terms {
		inMetadata, inBody := false, false
		for _, word := range group {
			if strings.Contains(metadata, word) {
				inMetadata = true
			}
			if strings.Contains(body, word) {
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
