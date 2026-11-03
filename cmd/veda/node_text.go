package main

import (
	"bytes"

	"github.com/yuin/goldmark/ast"
)

// nodeText returns the plain text of an inline container node (heading, code
// span, emphasis, link). It reproduces the output of goldmark v1.7.16
// Node.Text, which goldmark deprecated: children are concatenated and a soft
// line break adds a newline after its text segment.
func nodeText(n ast.Node, source []byte) []byte {
	switch v := n.(type) {
	case *ast.Text:
		return v.Segment.Value(source)
	case *ast.String:
		return v.Value
	case *ast.AutoLink:
		return v.Label(source)
	case *ast.RawHTML:
		return v.Segments.Value(source)
	}
	var buf bytes.Buffer
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		buf.Write(nodeText(c, source))
		if t, ok := c.(*ast.Text); ok && t.SoftLineBreak() {
			buf.WriteByte('\n')
		}
	}
	return buf.Bytes()
}
