package render

import (
	"strings"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

func TestMarkdownAndLinks(t *testing.T) {
	p := domain.Page{Path: "docs/index.md", Text: "# Heading\n\n[Other](other.md)\n\n<script>alert(1)</script>"}
	body, toc, e := Page(p)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(body), "<script>") {
		t.Fatal("unsafe html")
	}
	if !strings.Contains(string(body), "page=docs%2Fother.md") {
		t.Fatal("relative link")
	}
	if !strings.Contains(string(toc), `href="#heading"`) {
		t.Fatalf("bad TOC %s", toc)
	}
}
func TestUnicodeHeadingFragments(t *testing.T) {
	body, toc, err := Page(domain.Page{Path: "docs/a.md", Text: "## Раздел документации\n\n## Раздел документации"})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"раздел-документации", "раздел-документации-1"} {
		if !strings.Contains(string(body), `id="`+id+`"`) {
			t.Fatalf("missing heading %s: %s", id, body)
		}
		if !strings.Contains(string(toc), `href="#`+id+`"`) {
			t.Fatalf("missing TOC %s", id)
		}
	}
}

func TestMarkdownLinksStayInTheirSource(t *testing.T) {
	p := domain.Page{Path: "alpha/chapter/page.md", Text: "[Local](../guide.md#part)\n\n[Root](/docs/start.md)\n\n[Escape](../../beta/secret.md)\n\n[Encoded](%2e%2e/%2e%2e/beta/secret.md)\n\n[Outside](https://example.com/page)"}
	body, _, err := Page(p)
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	for _, expected := range []string{"page=alpha%2Fguide.md#part", "page=alpha%2Fdocs%2Fstart.md", "https://example.com/page"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("missing expected link %q", expected)
		}
	}
	if strings.Contains(html, "page=beta") || strings.Contains(html, "beta%2Fsecret.md") {
		t.Fatal("link escaped the source")
	}
}

func TestMarkdownImagesStayInTheirSource(t *testing.T) {
	p := domain.Page{Path: "alpha/chapter/page.md", Text: "![Local](../images/a.png)\n\n![Root](/images/b.png)\n\n![Escape](../../beta/image.png)\n\n![Encoded](%2e%2e/%2e%2e/beta/image.png)"}
	body, _, err := Page(p)
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	for _, expected := range []string{"/media/alpha/images%2Fa.png", "/media/alpha/images%2Fb.png"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("missing expected image %q", expected)
		}
	}
	if strings.Contains(html, "/media/beta/") || strings.Contains(html, "beta%2Fimage.png") {
		t.Fatal("image escaped the source")
	}
}

func TestMarkdownImagesUseTheRevisionDigest(t *testing.T) {
	old := strings.Repeat("a", 64)
	p := domain.Page{Path: "alpha/chapter/page.md", Text: "![Bound](../images/a.png)\n\n![Unbound](../images/b.png)", Assets: map[string]domain.BlobRef{"images/a.png": {SHA: old, MIME: "image/png"}}}
	body, _, err := Page(p)
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	if !strings.Contains(html, `src="/media/alpha/images%2Fa.png?v=`+old+`"`) {
		t.Fatal("bound image does not name its revision digest", html)
	}
	if !strings.Contains(html, `src="/media/alpha/images%2Fb.png"`) {
		t.Fatal("unbound image changed its URL", html)
	}
	p.Assets = nil
	body, _, _ = Page(p)
	if strings.Contains(string(body), "?v=") {
		t.Fatal("page without bound images names a digest")
	}
}
