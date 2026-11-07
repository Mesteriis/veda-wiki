package search

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// matchRunes bounds the line excerpt of a match.
const matchRunes = 200

// Match locates the best matching line of an article for a search result, so
// a reader can judge the result and continue with a bounded excerpt instead
// of reading the whole article.
type Match struct {
	Line    int    `json:"line"`              // 1-based line number
	Offset  int    `json:"offset"`            // code points before the line, as veda_read_excerpt counts
	Section string `json:"section,omitempty"` // nearest heading above the line
	Text    string `json:"text"`              // the line, cut to about matchRunes around the match
	// SHA is the matched revision's digest, the expected_sha256 that
	// veda_read_excerpt needs to start at Offset.
	SHA string `json:"sha256,omitempty"`
}

// FindMatch returns the line that contains the whole query, otherwise the
// earliest line matching the most query terms; nil when no line matches,
// for instance when only the title or path matched. Headings inside fenced
// code are not sections.
func FindMatch(text, query string, terms [][]string) *Match {
	query = lowerRunes(strings.TrimSpace(query))
	var best *Match
	bestGroups, bestAt := 0, 0
	section, fence := "", ""
	offset := 0
	for number, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if marker := fenceMarker(trimmed); marker != "" && (fence == "" || strings.HasPrefix(trimmed, fence)) {
			if fence == "" {
				fence = marker
			} else {
				fence = ""
			}
		} else if fence == "" && strings.HasPrefix(trimmed, "#") {
			if heading := strings.TrimSpace(strings.TrimLeft(trimmed, "#")); heading != "" {
				section = heading
			}
		}
		lowered := lowerRunes(line)
		groups, at := 0, -1
		if query != "" {
			if i := strings.Index(lowered, query); i >= 0 {
				groups, at = len(terms)+1, i
			}
		}
		if at < 0 {
			for _, group := range terms {
				for _, word := range group {
					if i := strings.Index(lowered, word); i >= 0 {
						groups++
						if at < 0 || i < at {
							at = i
						}
						break
					}
				}
			}
		}
		if groups > bestGroups {
			bestGroups, bestAt = groups, at
			best = &Match{Line: number + 1, Offset: offset, Section: section, Text: line}
			if groups > len(terms) {
				break
			}
		}
		offset += utf8.RuneCountInString(line) + 1
	}
	if best != nil {
		// lowerRunes keeps one rune per rune, so the byte index in the
		// lowered line converts to the same rune index in the original.
		best.Text = excerpt(best.Text, utf8.RuneCountInString(lowerRunes(best.Text)[:bestAt]))
	}
	return best
}

// lowerRunes lowercases rune by rune, keeping the rune count.
func lowerRunes(s string) string {
	return strings.Map(unicode.ToLower, s)
}

// fenceMarker returns the fence that a line opens or closes, or "".
func fenceMarker(line string) string {
	for _, marker := range []string{"```", "~~~"} {
		if strings.HasPrefix(line, marker) {
			return marker
		}
	}
	return ""
}

// excerpt trims line and cuts it to about matchRunes around rune index at.
func excerpt(line string, at int) string {
	runes := []rune(line)
	start, end := 0, len(runes)
	if end-start > matchRunes {
		start = max(0, at-matchRunes/4)
		end = min(len(runes), start+matchRunes)
		start = max(0, end-matchRunes)
	}
	out := strings.TrimSpace(string(runes[start:end]))
	if start > 0 {
		out = "…" + out
	}
	if end < len(runes) {
		out += "…"
	}
	return out
}
