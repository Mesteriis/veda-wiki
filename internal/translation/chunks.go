package translation

import (
	"errors"
	"slices"
	"strings"
	"unicode"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// This conservative script check cannot establish language or translation
// quality. It prevents an automatic draft containing only untranslated Latin
// prose from being accepted; code and URLs cannot satisfy the check.
func containsRussianProse(value string) bool {
	source := []byte(value)
	root := domain.Markdown.Parser().Parse(text.NewReader(source))
	var latin, cyrillic int
	ast.Walk(root, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := node.(type) {
		case *ast.FencedCodeBlock, *ast.CodeBlock, *ast.CodeSpan, *ast.AutoLink, *ast.RawHTML, *ast.HTMLBlock:
			return ast.WalkSkipChildren, nil
		case *ast.Text:
			for _, r := range string(n.Segment.Value(source)) {
				if unicode.Is(unicode.Cyrillic, r) && unicode.IsLetter(r) {
					cyrillic++
				} else if unicode.Is(unicode.Latin, r) && unicode.IsLetter(r) {
					latin++
				}
			}
		}
		return ast.WalkContinue, nil
	})
	return cyrillic >= 3 && cyrillic*5 >= (latin+cyrillic)*2
}

// Split only at top-level paragraph or heading boundaries. Code blocks, lists,
// tables and individual paragraphs stay intact. Reference-style Markdown can
// depend on definitions in another block, so reject any split that changes the
// sequence of protected Markdown when each chunk is parsed independently.
func translationChunks(original string, limit int) ([]string, error) {
	if limit <= 0 {
		return nil, errors.New("invalid translation limit")
	}
	if len(original) <= limit {
		return []string{original}, nil
	}
	root := domain.Markdown.Parser().Parse(text.NewReader([]byte(original)))
	boundaries := []int{0}
	for node := root.FirstChild(); node != nil; node = node.NextSibling() {
		if node.Kind() != ast.KindParagraph && node.Kind() != ast.KindHeading {
			continue
		}
		if node.Lines().Len() == 0 {
			continue
		}
		pos := node.Lines().At(0).Start
		if pos < 0 || pos > len(original) {
			return nil, errors.New("invalid markdown boundary")
		}
		pos = strings.LastIndex(original[:pos], "\n") + 1
		if pos > boundaries[len(boundaries)-1] {
			boundaries = append(boundaries, pos)
		}
	}
	var chunks []string
	for start := 0; start < len(original); {
		end := len(original)
		if end-start > limit {
			end = start
			for _, boundary := range boundaries {
				if boundary > start && boundary-start <= limit {
					end = boundary
				}
			}
			if end == start {
				return nil, errors.New("markdown block exceeds translation limit")
			}
		}
		chunks = append(chunks, original[start:end])
		start = end
	}
	// Goldmark can end an HTML block at a blank line even when an HTML tag
	// is still open. Only permit HTML in the final chunk: no translation
	// boundary may occur after an opening tag whose closure is uncertain.
	for _, chunk := range chunks[:len(chunks)-1] {
		hasHTML := false
		ast.Walk(domain.Markdown.Parser().Parse(text.NewReader([]byte(chunk))), func(node ast.Node, entering bool) (ast.WalkStatus, error) {
			if entering && (node.Kind() == ast.KindHTMLBlock || node.Kind() == ast.KindRawHTML) {
				hasHTML = true
				return ast.WalkStop, nil
			}
			return ast.WalkContinue, nil
		})
		if hasHTML {
			return nil, errors.New("HTML before final translation chunk requires manual handling")
		}
	}
	var protected []string
	for _, chunk := range chunks {
		protected = append(protected, protectedMarkdown(chunk)...)
	}
	if !slices.Equal(protectedMarkdown(original), protected) {
		return nil, errors.New("markdown references cross translation chunks")
	}
	return chunks, nil
}
