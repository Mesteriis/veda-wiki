package domain

import (
	"path"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
)

// MarkdownExtensions are the syntax extensions every article parser uses:
// GitHub-flavoured Markdown plus footnotes and definition lists.
var MarkdownExtensions = []goldmark.Extender{extension.GFM, extension.Footnote, extension.DefinitionList}

// Markdown is the GitHub-flavoured Markdown parser shared by rendering,
// link and dependency resolution.
var Markdown = goldmark.New(goldmark.WithExtensions(MarkdownExtensions...), goldmark.WithParserOptions(parser.WithAutoHeadingID()))

// RepositoryTarget resolves a link destination written in articlePath to a
// repository-relative path; a leading slash is relative to the repository
// root. The flag reports whether the result is a safe relative path.
func RepositoryTarget(articlePath, destination string) (string, bool) {
	if destination == "" {
		return "", false
	}
	var target string
	if strings.HasPrefix(destination, "/") {
		target = path.Clean(strings.TrimPrefix(destination, "/"))
	} else {
		target = path.Clean(path.Join(path.Dir(articlePath), destination))
	}
	return target, SafeRelative(target)
}

// A README has priority over index.md when resolving a directory. A fixed
// README dependency therefore cannot be shadowed by a later index.md.
func DirectoryReadmeTarget(articlePath, destination string) (string, bool) {
	if destination == "" {
		return "", false
	}
	base := path.Clean(path.Join(path.Dir(articlePath), destination))
	if strings.HasPrefix(destination, "/") {
		base = path.Clean(strings.TrimPrefix(destination, "/"))
	}
	if base == "." {
		return "README.md", true
	}
	if !SafeRelative(base) {
		return "", false
	}
	target := base + "/README.md"
	return target, SafeRelative(target)
}
