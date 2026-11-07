package render

import (
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

func TestFormatWarnings(t *testing.T) {
	text := "---\ntitle: X\n---\n# X\n\n" + // lines 1-4
		"<div class=\"box\">drop</div>\n\n" + // line 6
		"Inline <span>a</span> and <kbd>K</kbd>.\n\n" + // line 8
		"{% include note.html %} and {{ page.title }}\n\n" + // line 10
		"[escape](../../other/secret.md) [ok](guide.md) [web](https://example.com)\n\n" + // line 12
		"![img](../../x.png)\n\n" + // line 14
		"```\nplain\n```\n\n" + // line 16
		"`{{ in code }}`\n\n```bash\n{% not liquid %}\n```\n" // no warnings
	got := Warnings(domain.Page{Path: "docs/a/page.md", Text: text})
	want := []Warning{
		{Code: "raw_html", Line: 6, Message: "<div> is not allowed and is removed"},
		{Code: "raw_html", Line: 8, Message: "<span> is not allowed and is removed"},
		{Code: "liquid", Line: 10, Message: "Liquid tags are shown as plain text"},
		{Code: "link_outside_source", Line: 12, Message: "link ../../other/secret.md leaves the source and is removed"},
		{Code: "image_outside_source", Line: 14, Message: "image ../../x.png leaves the source and is not shown"},
		{Code: "code_without_language", Line: 16, Message: "name the language of the code block, for example ```bash"},
	}
	if len(got) != len(want) {
		t.Fatalf("warnings = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("warning %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestCleanArticleHasNoWarnings(t *testing.T) {
	text := "# Clean\n\n> [!NOTE]\n> Fine.\n\n<details>\n<summary>More</summary>\n\nText<br>\n\n</details>\n\n```mermaid\ngraph LR\n  A --> B\n```\n\n[up](../README.md) [root](/docs/x.md) [here](#clean)"
	if got := Warnings(domain.Page{Path: "docs/a/page.md", Text: text}); len(got) != 0 {
		t.Fatalf("unexpected warnings %+v", got)
	}
}

func TestRemoteImageWarning(t *testing.T) {
	got := Warnings(domain.Page{Path: "docs/a.md", Text: "# A\n\n![logo](https://example.com/logo.png)"})
	if len(got) != 1 || got[0].Code != "image_remote" || got[0].Line != 3 {
		t.Fatalf("warnings = %+v", got)
	}
}

func TestWarningsFlagAnInvalidVerifiedDate(t *testing.T) {
	got := Warnings(domain.Page{Path: "s/a.md", Text: "---\ntitle: A\nverified: yesterday\n---\n# A"})
	if len(got) != 1 || got[0].Code != "verified_invalid" || got[0].Line != 3 {
		t.Fatalf("%+v", got)
	}
	if got := Warnings(domain.Page{Path: "s/a.md", Text: "---\nverified: 2026-11-07\n---\n# A"}); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}
