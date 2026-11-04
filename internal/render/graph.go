package render

import (
	"net/url"
	"path"
	"sort"
	"strings"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// Edge is a directed link between two visible pages.
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// GraphEdges derives links only from the already-authorized, displayed snapshot.
func GraphEdges(pages []domain.Page) []Edge {
	visible := make(map[string]bool, len(pages))
	for _, page := range pages {
		visible[page.Path] = true
	}
	edges := make([]Edge, 0)
	seen := make(map[Edge]bool)
	for _, page := range pages {
		doc := domain.Markdown.Parser().Parse(text.NewReader([]byte(page.SearchText)))
		_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
			link, ok := n.(*ast.Link)
			if !entering || !ok {
				return ast.WalkContinue, nil
			}
			u, err := url.Parse(string(link.Destination))
			if err != nil || u.Scheme != "" || u.Host != "" || u.Path == "" {
				return ast.WalkContinue, nil
			}
			sourceID, target, valid := SourceTarget(page.Path, u.Path)
			if !valid {
				parts := strings.SplitN(page.Path, "/", 2)
				if len(parts) == 2 && domain.ValidID(parts[0]) && path.Clean(path.Join(path.Dir(parts[1]), u.Path)) == "." {
					sourceID, target, valid = parts[0], "", true
				}
			}
			if !valid {
				return ast.WalkContinue, nil
			}
			key := sourceID + "/" + target
			if !visible[key] {
				base := strings.TrimSuffix(key, "/")
				for _, suffix := range []string{"/README.md", "/index.md"} {
					if visible[base+suffix] {
						key = base + suffix
						break
					}
				}
			}
			edge := Edge{From: page.Path, To: key}
			if visible[key] && edge.From != edge.To && !seen[edge] {
				seen[edge] = true
				edges = append(edges, edge)
			}
			return ast.WalkContinue, nil
		})
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From == edges[j].From {
			return edges[i].To < edges[j].To
		}
		return edges[i].From < edges[j].From
	})
	return edges
}
