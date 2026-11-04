// Package render turns article Markdown into sanitized HTML with a table of
// contents, and builds the link graph between articles.
package render

import (
	"bytes"
	"fmt"
	"html/template"
	"net/url"
	"path"
	"strings"
	"unicode"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

func headingID(id any) string {
	if b, ok := id.([]byte); ok {
		return string(b)
	}
	return fmt.Sprint(id)
}

type unicodeIDs struct{ used map[string]bool }

// Generate implements parser.IDs: a heading ID that keeps Unicode letters and
// is unique within the document.
func (ids *unicodeIDs) Generate(value []byte, _ ast.NodeKind) []byte {
	var out strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(string(value))) {
		if unicode.IsSpace(r) {
			out.WriteByte('-')
		} else if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) || r == '-' || r == '_' {
			out.WriteRune(r)
		}
	}
	base := out.String()
	if base == "" {
		base = "section"
	}
	id := base
	for n := 1; ids.used[id]; n++ {
		id = fmt.Sprintf("%s-%d", base, n)
	}
	ids.used[id] = true
	return []byte(id)
}

// Put implements parser.IDs by reserving value.
func (ids *unicodeIDs) Put(value []byte) { ids.used[string(value)] = true }

// SourceTarget resolves repository paths independently of the source ID so a document
// cannot navigate into a different source using parent segments.
func SourceTarget(pagePath, destination string) (string, string, bool) {
	parts := strings.SplitN(pagePath, "/", 2)
	if len(parts) != 2 || !domain.ValidID(parts[0]) || destination == "" {
		return "", "", false
	}
	target, valid := domain.RepositoryTarget(parts[1], destination)
	return parts[0], target, valid
}

// Page renders the article Markdown to sanitized HTML and a table of
// contents.
func Page(p domain.Page) (template.HTML, template.HTML, error) {
	source := []byte(p.Text)
	doc := domain.Markdown.Parser().Parse(text.NewReader(source), parser.WithContext(parser.NewContext(parser.WithIDs(&unicodeIDs{used: map[string]bool{}}))))
	var toc strings.Builder
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if link, ok := n.(*ast.Link); ok {
			u, e := url.Parse(string(link.Destination))
			if e == nil && u.Scheme == "" && u.Host == "" && u.Path != "" {
				sourceID, target, valid := SourceTarget(p.Path, u.Path)
				if !valid {
					parts := strings.SplitN(p.Path, "/", 2)
					if len(parts) == 2 && domain.ValidID(parts[0]) {
						resolved := path.Clean(path.Join(path.Dir(parts[1]), u.Path))
						if strings.HasPrefix(u.Path, "/") {
							resolved = path.Clean(strings.TrimPrefix(u.Path, "/"))
						}
						if resolved == "." {
							sourceID, target, valid = parts[0], "", true
						}
					}
				}
				if !valid {
					link.Destination = nil
				} else {
					link.Destination = []byte("/?page=" + url.QueryEscape(sourceID+"/"+target))
					if u.Fragment != "" {
						link.Destination = append(link.Destination, []byte("#"+url.PathEscape(u.Fragment))...)
					}
				}
			}
		}
		if image, ok := n.(*ast.Image); ok {
			u, e := url.Parse(string(image.Destination))
			if e == nil && u.Scheme == "" && u.Host == "" {
				sourceID, target, valid := SourceTarget(p.Path, u.Path)
				if valid {
					image.Destination = []byte("/media/" + sourceID + "/" + url.PathEscape(target))
				} else {
					image.Destination = []byte("")
				}
			}
		}
		if h, ok := n.(*ast.Heading); ok {
			if id, exists := h.AttributeString("id"); exists {
				fmt.Fprintf(&toc, "<a class=level%d href=\"#%s\">%s</a>", h.Level, template.HTMLEscapeString(headingID(id)), template.HTMLEscapeString(string(NodeText(h, source))))
			}
		}
		return ast.WalkContinue, nil
	})
	var out bytes.Buffer
	err := domain.Markdown.Renderer().Render(&out, source, doc)
	return template.HTML(out.String()), template.HTML(toc.String()), err
}
