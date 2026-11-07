package search

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFindMatchPrefersTheWholeQueryThenMostTerms(t *testing.T) {
	text := "# Сервер\n\nnginx only here\n## Прокси\nNginx и Docker вместе\n```\n# not a heading\nnginx docker proxy\n```\nend"
	m := FindMatch(text, "docker nginx", Terms("docker nginx"))
	if m == nil || m.Line != 5 || m.Section != "Прокси" || m.Text != "Nginx и Docker вместе" {
		t.Fatalf("%+v", m)
	}
	// The offset is in code points and points at the line start.
	if got := string([]rune(text)[m.Offset:])[:len("Nginx")]; got != "Nginx" {
		t.Fatalf("offset %d lands on %q", m.Offset, got)
	}
	if m := FindMatch(text, "DOCKER вместе", nil); m == nil || m.Line != 5 {
		t.Fatalf("whole phrase, case-insensitive: %+v", m)
	}
	m = FindMatch(text, "nginx docker proxy", Terms("nginx docker proxy"))
	if m == nil || m.Line != 8 || m.Section != "Прокси" {
		t.Fatalf("a heading inside a fence must not become a section: %+v", m)
	}
	if FindMatch(text, "absent words", Terms("absent words")) != nil {
		t.Fatal("no line matches")
	}
}

func TestFindMatchCutsLongLinesAroundTheMatch(t *testing.T) {
	line := strings.Repeat("ё", 500) + " target " + strings.Repeat("ж", 500)
	m := FindMatch("# T\n"+line, "target", Terms("target"))
	if m == nil || !strings.Contains(m.Text, "target") || !strings.HasPrefix(m.Text, "…") || !strings.HasSuffix(m.Text, "…") ||
		utf8.RuneCountInString(m.Text) > matchRunes+2 {
		t.Fatalf("%d %q", utf8.RuneCountInString(m.Text), m.Text)
	}
	if short := FindMatch("one target line", "target", Terms("target")); short.Text != "one target line" || short.Offset != 0 {
		t.Fatalf("%+v", short)
	}
}

func TestPageMatchesOnlyThePage(t *testing.T) {
	var candidates []Candidate
	for _, p := range []string{"a.md", "b.md", "c.md"} {
		candidates = append(candidates, Candidate{hit: Hit{SourceID: "s", Path: p, Title: p}, text: "# " + p + "\n\nvps here", sha: "sha-" + p})
	}
	hits, total := Page(candidates, "vps", 1, 1)
	if total != 3 || len(hits) != 1 || hits[0].Path != "b.md" || hits[0].Match == nil || hits[0].Match.Line != 3 || hits[0].Match.SHA != "sha-b.md" {
		t.Fatalf("%d %+v", total, hits)
	}
	if hits, total := Page(candidates, "vps", 5, 10); total != 3 || len(hits) != 0 {
		t.Fatal("offset past the end", total, hits)
	}
	for _, hit := range Rank(candidates, "vps") {
		if hit.Match != nil {
			t.Fatal("Rank must not scan lines")
		}
	}
}
