package render

import (
	"bytes"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// Warning names Markdown that VEDA removes or does not render as the author
// probably meant. Line is 1-based in the article text, front matter included.
type Warning struct {
	Code    string `json:"code"`
	Line    int    `json:"line"`
	Message string `json:"message"`
}

// maxWarnings bounds the list returned with a write.
const maxWarnings = 50

var tagName = regexp.MustCompile(`^<([A-Za-z][A-Za-z0-9-]*)`)

// Warnings lists what Page would drop or show differently: raw HTML outside
// the allow list, Liquid tags, links and images that leave the source, remote
// images (the page policy blocks them) and code fences without a language.
func Warnings(p domain.Page) []Warning {
	front, body := domain.SplitFrontMatter(p.Text)
	offset := 0
	if front != "" || len(body) != len(p.Text) {
		offset = strings.Count(p.Text[:len(p.Text)-len(body)], "\n")
	}
	source := []byte(body)
	line := func(pos int) int { return offset + 1 + bytes.Count(source[:min(pos, len(source))], []byte("\n")) }
	var out []Warning
	add := func(code string, pos int, format string, args ...any) {
		if len(out) < maxWarnings {
			out = append(out, Warning{Code: code, Line: line(pos), Message: fmt.Sprintf(format, args...)})
		}
	}
	liquidLines := map[int]bool{}
	doc := domain.Markdown.Parser().Parse(text.NewReader(source))
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.CodeSpan:
			return ast.WalkSkipChildren, nil
		case *ast.FencedCodeBlock:
			if n.Info == nil && n.Lines().Len() > 0 {
				add("code_without_language", n.Lines().At(0).Start-1, "name the language of the code block, for example ```bash")
			}
		case *ast.HTMLBlock:
			var raw []byte
			for i := 0; i < n.Lines().Len(); i++ {
				segment := n.Lines().At(i)
				raw = append(raw, segment.Value(source)...)
			}
			if n.Lines().Len() > 0 {
				htmlWarnings(raw, n.Lines().At(0).Start, add)
			}
		case *ast.RawHTML:
			if n.Segments.Len() > 0 {
				first := n.Segments.At(0)
				htmlWarnings(n.Segments.Value(source), first.Start, add)
			}
		case *ast.Text:
			value := n.Segment.Value(source)
			if (bytes.Contains(value, []byte("{%")) || bytes.Contains(value, []byte("{{"))) && !liquidLines[line(n.Segment.Start)] {
				liquidLines[line(n.Segment.Start)] = true
				add("liquid", n.Segment.Start, "Liquid tags are shown as plain text")
			}
		case *ast.Link:
			u, err := url.Parse(string(n.Destination))
			if err == nil && u.Scheme == "" && u.Host == "" && u.Path != "" {
				if _, _, ok := sourceLink(p.Path, u.Path); !ok {
					add("link_outside_source", inlineStart(n), "link %s leaves the source and is removed", n.Destination)
				}
			}
		case *ast.Image:
			u, err := url.Parse(string(n.Destination))
			switch {
			case err == nil && u.Scheme == "" && u.Host == "":
				if _, _, ok := SourceTarget(p.Path, u.Path); !ok {
					add("image_outside_source", inlineStart(n), "image %s leaves the source and is not shown", n.Destination)
				}
			case err == nil && u.Scheme != "data":
				add("image_remote", inlineStart(n), "remote image %s is blocked; keep images in the repository", n.Destination)
			}
		}
		return ast.WalkContinue, nil
	})
	return out
}

// htmlWarnings reports each opening tag in raw that the allow list drops;
// comments are removed silently.
func htmlWarnings(raw []byte, start int, add func(string, int, string, ...any)) {
	comments := htmlComment.FindAllIndex(raw, -1)
	inComment := func(at int) bool {
		for _, c := range comments {
			if at >= c[0] && at < c[1] {
				return true
			}
		}
		return false
	}
	for _, m := range htmlTag.FindAllIndex(raw, -1) {
		tag := raw[m[0]:m[1]]
		if inComment(m[0]) || bytes.HasPrefix(tag, []byte("</")) || bytes.HasPrefix(tag, []byte("<!")) {
			continue
		}
		if _, ok := allowedHTMLTag(tag); ok {
			continue
		}
		name := tagName.FindSubmatch(tag)
		if name == nil {
			continue
		}
		lower := strings.ToLower(string(name[1]))
		// Block lines are contiguous in the source outside containers, so the
		// offset in raw is the offset from the first line.
		pos := start + m[0]
		if allowedTag.Match([]byte("<" + lower + ">")) {
			add("raw_html", pos, "attributes on <%s> are not allowed; the tag is removed", lower)
		} else {
			add("raw_html", pos, "<%s> is not allowed and is removed", lower)
		}
	}
}

// inlineStart is the source offset of an inline node: its first text, else
// the first line of the block that holds it.
func inlineStart(n ast.Node) int {
	start := -1
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if t, ok := c.(*ast.Text); ok && entering && start < 0 {
			start = t.Segment.Start
			return ast.WalkStop, nil
		}
		return ast.WalkContinue, nil
	})
	for p := n.Parent(); start < 0 && p != nil; p = p.Parent() {
		if p.Type() == ast.TypeBlock && p.Lines().Len() > 0 {
			start = p.Lines().At(0).Start
		}
	}
	return max(start, 0)
}
