package domain

import (
	"strings"
	"unicode/utf8"
)

// Section is one ATX heading of an article. Offsets and sizes count Unicode
// code points of the whole text, front matter included, as excerpts do.
type Section struct {
	Level      int    `json:"level"`
	Title      string `json:"title"`
	Line       int    `json:"line"`       // 1-based
	Offset     int    `json:"offset"`     // code points before the heading line
	Characters int    `json:"characters"` // up to the next heading of the same or a higher level
}

// Outline lists the ATX headings of text outside front matter and fenced
// code, in order. A section spans its subsections.
func Outline(text string) []Section {
	_, body := SplitFrontMatter(text)
	skip := len(text) - len(body) // the front matter block with its delimiters
	var sections []Section
	fence := ""
	offset, consumed := 0, 0
	for number, line := range strings.Split(text, "\n") {
		start := consumed
		consumed += len(line) + 1
		lineRunes := utf8.RuneCountInString(line)
		at := offset
		offset += lineRunes + 1
		if start < skip {
			continue
		}
		trimmed := strings.TrimRight(line, "\r")
		indented := strings.TrimLeft(trimmed, " ")
		if len(trimmed)-len(indented) > 3 {
			continue
		}
		if marker := fenceOpening(indented); marker != "" {
			switch {
			case fence == "":
				fence = marker
			case strings.HasPrefix(indented, fence) && strings.Trim(indented, fence[:1]) == "":
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}
		if level, title, ok := atxHeading(indented); ok {
			sections = append(sections, Section{Level: level, Title: title, Line: number + 1, Offset: at})
		}
	}
	total := utf8.RuneCountInString(text)
	for i := range sections {
		end := total
		for _, next := range sections[i+1:] {
			if next.Level <= sections[i].Level {
				end = next.Offset
				break
			}
		}
		sections[i].Characters = end - sections[i].Offset
	}
	return sections
}

// fenceOpening returns the fence characters a line starts with: three or
// more backticks or tildes.
func fenceOpening(line string) string {
	for _, c := range []string{"`", "~"} {
		n := len(line) - len(strings.TrimLeft(line, c))
		if n >= 3 {
			return strings.Repeat(c, n)
		}
	}
	return ""
}

// atxHeading parses "# Title", "## Title ##" and so on.
func atxHeading(line string) (int, string, bool) {
	level := len(line) - len(strings.TrimLeft(line, "#"))
	if level < 1 || level > 6 {
		return 0, "", false
	}
	rest := line[level:]
	if rest != "" && rest[0] != ' ' && rest[0] != '\t' {
		return 0, "", false
	}
	title := strings.TrimSpace(rest)
	if closing := strings.TrimRight(title, "#"); closing != title && (closing == "" || strings.HasSuffix(closing, " ")) {
		title = strings.TrimSpace(closing)
	}
	return level, title, true
}
