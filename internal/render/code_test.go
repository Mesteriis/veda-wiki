package render

import (
	"strings"
	"testing"
)

func TestCodeBlocksAreHighlightedWithClasses(t *testing.T) {
	html := render(t, "```go\nfunc main() { fmt.Println(\"<b>\") }\n```\n\n```\nplain <i>\n```")
	for _, want := range []string{`class="chroma"`, `<span class="kd">func</span>`, "&lt;b&gt;", "plain &lt;i&gt;"} {
		if !strings.Contains(html, want) {
			t.Fatalf("missing %q in %s", want, html)
		}
	}
	if strings.Contains(html, "style=") || strings.Contains(html, "<b>") || strings.Contains(html, "<i>") {
		t.Fatalf("inline style or unescaped code: %s", html)
	}
}

func TestMermaidBlocksStayEscapedSource(t *testing.T) {
	html := render(t, "```mermaid\ngraph LR\n  A[\"<img src=x onerror=alert(1)>\"] --> B\n```")
	if !strings.Contains(html, `<pre class="mermaid">graph LR`) || !strings.Contains(html, "&lt;img src=x") {
		t.Fatalf("mermaid source not kept: %s", html)
	}
	if strings.Contains(html, "<img") || strings.Contains(html, "chroma") {
		t.Fatalf("mermaid block unsafe or highlighted: %s", html)
	}
}
