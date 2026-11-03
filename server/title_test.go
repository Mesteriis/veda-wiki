package main

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
		if got := title(tc.text, tc.path); got != tc.want {
			t.Fatalf("title = %q, want %q", got, tc.want)
		}
	}
}
