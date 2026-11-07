package render

import (
	"net/url"
	"sort"
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// Issue is one finding of a source check: a formatting warning or a link
// or image whose target does not exist in the source.
type Issue struct {
	Path string `json:"path"` // article path within the source
	Warning
}

// maxIssues bounds a source check.
const maxIssues = 2000

// CheckSource checks every page of sourceID in pages, the visible snapshot:
// the formatting warnings a write would return, links to pages that are not
// in the snapshot (directory links count when the directory has a README.md
// or index.md) and images missing from assets, the source's stored images.
// Issues are ordered by path and line; at most maxIssues are returned, and
// total counts them all.
func CheckSource(pages []domain.Page, sourceID string, assets map[string]domain.BlobRef) (issues []Issue, checked, total int) {
	visible := make(map[string]bool, len(pages))
	for _, p := range pages {
		visible[p.Path] = true
	}
	prefix := sourceID + "/"
	for _, p := range pages {
		if !strings.HasPrefix(p.Path, prefix) {
			continue
		}
		checked++
		page := domain.Page{Path: p.Path, Text: p.SearchText}
		found := append(Warnings(page), missingTargets(page, visible, assets)...)
		for _, w := range found {
			total++
			if len(issues) < maxIssues {
				issues = append(issues, Issue{Path: strings.TrimPrefix(p.Path, prefix), Warning: w})
			}
		}
	}
	sort.SliceStable(issues, func(i, j int) bool {
		if issues[i].Path != issues[j].Path {
			return issues[i].Path < issues[j].Path
		}
		return issues[i].Line < issues[j].Line
	})
	return issues, checked, total
}

// missingTargets reports in-source links and images whose targets are absent.
// Targets outside the source are already formatting warnings.
func missingTargets(p domain.Page, visible map[string]bool, assets map[string]domain.BlobRef) []Warning {
	source, line := markdownBody(p.Text)
	var out []Warning
	doc := domain.Markdown.Parser().Parse(text.NewReader(source))
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering || len(out) >= maxWarnings {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.CodeSpan, *ast.FencedCodeBlock, *ast.CodeBlock:
			return ast.WalkSkipChildren, nil
		case *ast.Link:
			u, err := url.Parse(string(n.Destination))
			if err != nil || u.Scheme != "" || u.Host != "" || u.Path == "" {
				break
			}
			sourceID, target, ok := sourceLink(p.Path, u.Path)
			if _, stored := assets[target]; ok && !stored && !pageExists(visible, sourceID, target) {
				out = append(out, Warning{Code: "link_missing", Line: line(inlineStart(n)),
					Message: "link " + string(n.Destination) + " points to a page that does not exist"})
			}
		case *ast.Image:
			u, err := url.Parse(string(n.Destination))
			if err != nil || u.Scheme != "" || u.Host != "" {
				break
			}
			if _, target, ok := SourceTarget(p.Path, u.Path); ok {
				if _, stored := assets[target]; !stored {
					out = append(out, Warning{Code: "image_missing", Line: line(inlineStart(n)),
						Message: "image " + string(n.Destination) + " is not in the source"})
				}
			}
		}
		return ast.WalkContinue, nil
	})
	return out
}

// pageExists resolves a link target as the reading interface does: the page
// itself, or a directory's README.md, then index.md.
func pageExists(visible map[string]bool, sourceID, target string) bool {
	key := sourceID + "/" + target
	if target != "" && visible[key] {
		return true
	}
	base := strings.TrimSuffix(key, "/")
	return visible[base+"/README.md"] || visible[base+"/index.md"]
}
