package render

import (
	"bytes"
	"html"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/util"
)

// codeHighlighting colours fenced code on the server with CSS classes only:
// the page's Content-Security-Policy forbids inline styles.
var codeHighlighting = highlighting.NewHighlighting(
	highlighting.WithFormatOptions(chromahtml.WithClasses(true)),
)

// KindMermaid is a fenced ```mermaid block; the browser draws it.
var KindMermaid = ast.NewNodeKind("Mermaid")

// mermaidBlock keeps the diagram source of a ```mermaid fence.
type mermaidBlock struct {
	ast.BaseBlock
	source []byte
}

func (*mermaidBlock) Kind() ast.NodeKind { return KindMermaid }

func (n *mermaidBlock) Dump(source []byte, level int) { ast.DumpHelper(n, source, level, nil, nil) }

// markMermaid replaces ```mermaid fences with mermaid blocks so they are not
// highlighted as code.
func markMermaid(doc ast.Node, source []byte) {
	var fences []*ast.FencedCodeBlock
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if f, ok := n.(*ast.FencedCodeBlock); ok && entering && string(f.Language(source)) == "mermaid" {
			fences = append(fences, f)
		}
		return ast.WalkContinue, nil
	})
	for _, f := range fences {
		var text []byte
		for i := 0; i < f.Lines().Len(); i++ {
			line := f.Lines().At(i)
			text = append(text, line.Value(source)...)
		}
		f.Parent().ReplaceChild(f.Parent(), f, &mermaidBlock{source: text})
	}
}

type mermaidRenderer struct{}

// RegisterFuncs writes the escaped source in <pre class="mermaid">; without
// JavaScript the reader still sees the diagram text.
func (mermaidRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(KindMermaid, func(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			text := bytes.TrimRight(node.(*mermaidBlock).source, "\n")
			_, _ = w.WriteString(`<pre class="mermaid">` + html.EscapeString(string(text)) + "</pre>\n")
		}
		return ast.WalkSkipChildren, nil
	})
}
