package main

import (
	"bytes"
	"testing"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

func TestNodeTextMatchesGoldmarkNodeText(t *testing.T) {
	sources := []string{
		"# Plain *emphasis* and **strong** and ***both***\n",
		"## Code `inline` span and [link *em*](http://x.test) and ![alt *t*](i.png)\n",
		"### Entities &amp; &copy; &#35; and escapes \\*star\\* \\# hash\n",
		"Setext heading\nsecond line\n===\n",
		"Second setext\ncontinued here  \nand a hard break\n---\n",
		"# Auto <http://auto.test> and <b>raw</b> html\n",
		"Paragraph with `` ` `` and ` lead` and `trail ` and `  both  ` and ``a`b``\n\n`` `tick` ``\n\n`multi\nline code`\n",
		"- item with `code` and\n  continuation `x\n  y`\n\n> quote `q`\n",
		"# Heading with `` double `tick `` and trailing\n",
	}
	headings, spans := 0, 0
	for _, src := range sources {
		source := []byte(src)
		doc := markdown.Parser().Parse(text.NewReader(source))
		err := ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
			if !entering {
				return ast.WalkContinue, nil
			}
			switch n.(type) {
			case *ast.Heading:
				headings++
			case *ast.CodeSpan:
				spans++
			default:
				return ast.WalkContinue, nil
			}
			//lint:ignore SA1019 the deprecated method is the oracle for the replacement helper
			want := n.Text(source)
			if got := nodeText(n, source); !bytes.Equal(got, want) {
				t.Errorf("%T in %q: got %q, want %q", n, src, got, want)
			}
			return ast.WalkContinue, nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if headings < 6 || spans < 8 {
		t.Fatalf("coverage too thin: %d headings, %d code spans", headings, spans)
	}
}
