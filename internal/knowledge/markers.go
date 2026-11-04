package knowledge

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MarkersVersion versions the marker lists below. Any change to them must
// bump it, which changes ResolveVersion and the pipeline fingerprint.
const MarkersVersion = "markers-v2"

// markerLists are the ru/en words that make a claim uncertain, by category,
// in output order. Matching is case-insensitive on whole words.
var markerLists = []struct {
	category string
	words    []string
}{
	{"negation", []string{
		"не", "нет", "ни", "никогда", "больше не", "без",
		"not", "no", "never", "no longer", "without", "cannot", "can't", "don't", "doesn't",
		"isn't", "aren't", "wasn't", "weren't", "won't",
	}},
	{"plan", []string{
		"планируется", "планируем", "планирую", "запланировано", "будет", "будут", "в планах", "собираемся",
		"plan", "plans", "planned", "will", "going to", "todo", "roadmap",
	}},
	{"example", []string{
		"например", "к примеру", "пример", "допустим",
		"e.g.", "for example", "for instance", "example", "such as",
	}},
	{"past", []string{
		"раньше", "ранее", "прежде", "когда-то", "был", "была", "было", "были",
		"previously", "formerly", "used to", "was", "were", "in the past",
	}},
}

type markerMatch struct {
	label      string
	order      int
	start, end int
}

// Markers lists the negation, plan, example and past markers in sentence as
// "category:marker", grouped by category in the order negation, plan,
// example, past. A marker inside a longer matched marker ("no" in "no
// longer") is not listed again. It makes no other classification.
func Markers(sentence string) []string {
	lower := strings.ToLower(strings.ReplaceAll(sentence, "’", "'"))
	var found []markerMatch
	order := 0
	for _, list := range markerLists {
		for _, word := range list.words {
			for from := 0; ; {
				i := strings.Index(lower[from:], word)
				if i < 0 {
					break
				}
				start, end := from+i, from+i+len(word)
				if wordBoundary(lower, start, end) {
					found = append(found, markerMatch{label: list.category + ":" + word, order: order, start: start, end: end})
				}
				from = start + 1
			}
			order++
		}
	}
	// Longest first, so a marker inside a longer one is dropped.
	sort.SliceStable(found, func(i, j int) bool { return found[i].end-found[i].start > found[j].end-found[j].start })
	var kept []markerMatch
	for _, m := range found {
		overlaps := false
		for _, k := range kept {
			if m.start < k.end && k.start < m.end {
				overlaps = true
				break
			}
		}
		if !overlaps {
			kept = append(kept, m)
		}
	}
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].order < kept[j].order })
	var out []string
	seen := map[string]bool{}
	for _, m := range kept {
		if !seen[m.label] {
			seen[m.label] = true
			out = append(out, m.label)
		}
	}
	return out
}

// wordBoundary reports that the match is a whole word or phrase, following
// the Unicode word boundary rules (UAX #29) that matter here: letters,
// marks, digits and connector punctuation ("_") continue a word, and so do
// ".", "'", ":" and "·" between two word characters, so "example.com",
// "nowhere" and "todo_list" are single words.
func wordBoundary(s string, start, end int) bool {
	if start > 0 {
		r, size := utf8.DecodeLastRuneInString(s[:start])
		if isWordRune(r) {
			return false
		}
		if midWord(r) && start-size > 0 {
			if before, _ := utf8.DecodeLastRuneInString(s[:start-size]); isWordRune(before) {
				return false
			}
		}
	}
	if end < len(s) {
		r, size := utf8.DecodeRuneInString(s[end:])
		if isWordRune(r) {
			return false
		}
		if midWord(r) && end+size < len(s) {
			if after, _ := utf8.DecodeRuneInString(s[end+size:]); isWordRune(after) {
				return false
			}
		}
	}
	return true
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) || unicode.Is(unicode.Pc, r)
}

func midWord(r rune) bool { return r == '.' || r == '\'' || r == ':' || r == '\u00b7' }
