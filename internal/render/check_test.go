package render

import (
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

func TestCheckSourceFindsMissingTargetsAndWarnings(t *testing.T) {
	pages := []domain.Page{
		{Path: "w/README.md", SearchText: "# Home\n\n[ok](guide.md) [dir](net/) [root](/) [anchor](#x)\n[gone](missing.md)\n![pic](img/a.png) ![lost](img/b.png)\n`[code](nowhere.md)`\n[file](img/a.png)\n"},
		{Path: "w/guide.md", SearchText: "---\ntitle: G\n---\n# Guide\n\n<div>x</div>\n\n[up](../outside.md)\n"},
		{Path: "w/net/index.md", SearchText: "# Net"},
		{Path: "other/README.md", SearchText: "[gone](nope.md)"},
	}
	issues, checked, total := CheckSource(pages, "w", map[string]domain.BlobRef{"img/a.png": {}})
	if checked != 3 || total != len(issues) {
		t.Fatalf("checked %d total %d", checked, total)
	}
	got := map[string]int{}
	for _, i := range issues {
		got[i.Path+" "+i.Code] = i.Line
	}
	want := map[string]int{
		"README.md link_missing":       4,
		"README.md image_missing":      5,
		"guide.md raw_html":            6,
		"guide.md link_outside_source": 8,
	}
	if len(got) != len(want) {
		t.Fatalf("%v", issues)
	}
	for k, line := range want {
		if got[k] != line {
			t.Fatalf("%s: line %d in %v", k, got[k], issues)
		}
	}
	if issues[0].Path != "README.md" || issues[len(issues)-1].Path != "guide.md" {
		t.Fatal("issues must be ordered by path")
	}
}
