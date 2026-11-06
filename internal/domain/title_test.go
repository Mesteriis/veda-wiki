package domain

import (
	"strings"
	"testing"
)

func TestTitleMatchesFirstMarkdownH1OrPath(t *testing.T) {
	for _, tc := range []struct{ text, path, want string }{
		{"# First\n# Second", "folder/doc.md", "First"},
		{"## Subheading\n\n#  Trimmed  \r\ntext", "folder/doc.md", "Trimmed"},
		{"plain\n# Last", "folder/doc.md", "Last"},
		{"plain\n## Other\n", "folder/doc.md", "doc.md"},
		{"", "folder/doc.md", "doc.md"},
		{"# \n# Other", "folder/doc.md", ""},
		{strings.Repeat("body\n", 100000) + "# Final", "folder/doc.md", "Final"},
	} {
		if got := Title(tc.text, tc.path); got != tc.want {
			t.Fatalf("title = %q, want %q", got, tc.want)
		}
	}
}

func TestTitlePrefersFrontMatter(t *testing.T) {
	for _, tc := range []struct{ text, want string }{
		{"---\ntitle: Из шапки\n---\n# Заголовок", "Из шапки"},
		{"---\r\nlayout: page\r\ntitle: \"В кавычках\"\r\n---\r\n# H", "В кавычках"},
		{"---\nlayout: page\n---\n# Заголовок", "Заголовок"},
		{"---\ntitle: Без конца\n# H", "H"},
		{"text\n---\ntitle: Not front\n---\n# H", "H"},
	} {
		if got := Title(tc.text, "a/b.md"); got != tc.want {
			t.Fatalf("Title(%q) = %q, want %q", tc.text, got, tc.want)
		}
	}
}

func TestSplitFrontMatter(t *testing.T) {
	front, body := SplitFrontMatter("---\ntitle: X\n...\nBody")
	if front != "title: X\n" || body != "Body" {
		t.Fatalf("front=%q body=%q", front, body)
	}
	if front, body := SplitFrontMatter("---\nunterminated"); front != "" || body != "---\nunterminated" {
		t.Fatalf("unterminated split: %q %q", front, body)
	}
}
