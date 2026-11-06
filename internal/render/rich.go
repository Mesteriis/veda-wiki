package render

import (
	"bytes"
	"html"
	"regexp"
	"slices"
	"strings"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/util"
)

// htmlRenderer renders parsed articles. It replaces goldmark's raw HTML
// output with an allow list, so the article HTML stays sanitized, and
// highlights fenced code.
var htmlRenderer = goldmark.New(
	goldmark.WithExtensions(slices.Concat(domain.MarkdownExtensions, []goldmark.Extender{codeHighlighting})...),
	goldmark.WithRendererOptions(renderer.WithNodeRenderers(util.Prioritized(allowedHTML{}, 100), util.Prioritized(mermaidRenderer{}, 100))),
).Renderer()

var (
	htmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)
	htmlTag     = regexp.MustCompile(`<[^<>]*>`)
	// allowedTag lists the raw HTML an article may use: collapsible blocks
	// and the inline elements GitHub renders too. "open" on <details> is the
	// only attribute; anything else drops the whole tag.
	allowedTag = regexp.MustCompile(`(?i)^<(/?)(details|summary|kbd|sub|sup|br)(\s+open)?\s*/?>$`)
)

// filterHTML keeps allowed tags in a normalised form and escapes the text
// between them; every other tag and comment is dropped.
func filterHTML(raw []byte) []byte {
	raw = htmlComment.ReplaceAll(raw, nil)
	var out bytes.Buffer
	text := func(b []byte) { out.WriteString(html.EscapeString(html.UnescapeString(string(b)))) }
	last := 0
	for _, m := range htmlTag.FindAllIndex(raw, -1) {
		text(raw[last:m[0]])
		last = m[1]
		tag := allowedTag.FindSubmatch(raw[m[0]:m[1]])
		if tag == nil {
			continue
		}
		name := strings.ToLower(string(tag[2]))
		if len(tag[3]) > 0 && (name != "details" || len(tag[1]) > 0) {
			continue
		}
		switch {
		case name == "br":
			out.WriteString("<br>")
		case len(tag[1]) > 0:
			out.WriteString("</" + name + ">")
		case len(tag[3]) > 0:
			out.WriteString("<details open>")
		default:
			out.WriteString("<" + name + ">")
		}
	}
	text(raw[last:])
	return out.Bytes()
}

type allowedHTML struct{}

func (allowedHTML) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindHTMLBlock, func(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		n := node.(*ast.HTMLBlock)
		var raw []byte
		for i := 0; i < n.Lines().Len(); i++ {
			line := n.Lines().At(i)
			raw = append(raw, line.Value(source)...)
		}
		if n.HasClosure() {
			raw = append(raw, n.ClosureLine.Value(source)...)
		}
		if out := bytes.TrimSpace(filterHTML(raw)); len(out) > 0 {
			_, _ = w.Write(append(out, '\n'))
		}
		return ast.WalkContinue, nil
	})
	reg.Register(ast.KindRawHTML, func(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			n := node.(*ast.RawHTML)
			var raw []byte
			for i := 0; i < n.Segments.Len(); i++ {
				segment := n.Segments.At(i)
				raw = append(raw, segment.Value(source)...)
			}
			_, _ = w.Write(filterHTML(raw))
		}
		return ast.WalkSkipChildren, nil
	})
}

var alertMarker = regexp.MustCompile(`(?i)^\[!(note|tip|important|warning|caution)\][ \t]*$`)

var alertTitles = map[string]string{
	"note":      "Примечание",
	"tip":       "Совет",
	"important": "Важно",
	"warning":   "Внимание",
	"caution":   "Осторожно",
}

// markAlerts turns GitHub alert blockquotes ("> [!NOTE]") into callouts:
// the marker line becomes a title and the blockquote gets alert classes.
func markAlerts(doc ast.Node, source []byte) {
	var quotes []*ast.Blockquote
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if q, ok := n.(*ast.Blockquote); ok && entering {
			quotes = append(quotes, q)
		}
		return ast.WalkContinue, nil
	})
	for _, q := range quotes {
		p, ok := q.FirstChild().(*ast.Paragraph)
		if !ok || p.Lines().Len() == 0 {
			continue
		}
		line := p.Lines().At(0)
		m := alertMarker.FindSubmatch(bytes.TrimRight(line.Value(source), "\r\n"))
		if m == nil {
			continue
		}
		for c := p.FirstChild(); c != nil; {
			t, ok := c.(*ast.Text)
			if !ok || t.Segment.Start >= line.Stop {
				break
			}
			next := c.NextSibling()
			p.RemoveChild(p, c)
			c = next
		}
		if p.FirstChild() == nil {
			q.RemoveChild(q, p)
		}
		kind := strings.ToLower(string(m[1]))
		title := ast.NewParagraph()
		title.SetAttributeString("class", []byte("markdown-alert-title"))
		title.AppendChild(title, ast.NewString([]byte(alertTitles[kind])))
		q.InsertBefore(q, q.FirstChild(), title)
		q.SetAttributeString("class", []byte("markdown-alert markdown-alert-"+kind))
	}
}
