package render

import (
	"strings"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

func render(t *testing.T, text string) string {
	t.Helper()
	body, _, err := Page(domain.Page{Path: "docs/a.md", Text: text})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestCollapsibleBlocksRender(t *testing.T) {
	html := render(t, "<details open>\n<summary>Подробнее & ещё</summary>\n\nТекст **жирный**\n\n</details>\n\nНажмите <kbd>Ctrl</kbd>+<kbd>K</kbd>, H<sub>2</sub>O")
	for _, want := range []string{"<details open>", "<summary>Подробнее &amp; ещё</summary>", "<strong>жирный</strong>", "</details>", "<kbd>Ctrl</kbd>", "<sub>2</sub>"} {
		if !strings.Contains(html, want) {
			t.Fatalf("missing %q in %s", want, html)
		}
	}
}

func TestRawHTMLOutsideTheAllowListIsDropped(t *testing.T) {
	html := render(t, "<details ontoggle=\"alert(1)\">\n<summary><script>alert(2)</script>X</summary>\n<img src=x onerror=alert(3)>\n<!-- <b>hidden</b> -->\n</details>\n\n<a href=\"javascript:alert(4)\">link</a> <iframe src=\"https://example.com\"></iframe> <details open onclick=x>")
	for _, bad := range []string{"<script", "ontoggle", "onerror", "onclick", "<img", "<iframe", "javascript:", "hidden", "<a "} {
		if strings.Contains(html, bad) {
			t.Fatalf("unsafe %q kept in %s", bad, html)
		}
	}
	if !strings.Contains(html, "<summary>") {
		t.Fatalf("allowed tag dropped: %s", html)
	}
}

func TestGitHubAlerts(t *testing.T) {
	html := render(t, "> [!WARNING]\n> Проверьте **бэкап**.\n\n> [!note]\n\n> Отдельный абзац\n\n> [!FOO]\n> Обычная цитата")
	for _, want := range []string{
		`<blockquote class="markdown-alert markdown-alert-warning">`,
		`<p class="markdown-alert-title">Внимание</p>`,
		"Проверьте <strong>бэкап</strong>.",
		`<blockquote class="markdown-alert markdown-alert-note">`,
		"[!FOO]",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("missing %q in %s", want, html)
		}
	}
	if strings.Contains(html, "[!WARNING]") || strings.Contains(html, "[!note]") {
		t.Fatalf("alert marker shown: %s", html)
	}
}

func TestFootnotesAndDefinitionLists(t *testing.T) {
	html := render(t, "Факт[^src].\n\n[^src]: Источник [страница](other.md).\n\nТермин\n: Определение")
	for _, want := range []string{`class="footnotes"`, `id="fn:1"`, "page=docs%2Fother.md", "<dl>", "<dt>Термин</dt>", "<dd>Определение</dd>"} {
		if !strings.Contains(html, want) {
			t.Fatalf("missing %q in %s", want, html)
		}
	}
}

func TestFrontMatterIsHidden(t *testing.T) {
	html := render(t, "---\ntitle: Гайд\nlayout: page\n---\n\n# Гайд\n\nТекст")
	if strings.Contains(html, "layout") || strings.Contains(html, "<hr") {
		t.Fatalf("front matter rendered: %s", html)
	}
	if !strings.Contains(html, "Текст") {
		t.Fatalf("body lost: %s", html)
	}
}
