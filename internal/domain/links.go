package domain

import (
	"path"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
)

var Markdown = goldmark.New(goldmark.WithExtensions(extension.GFM), goldmark.WithParserOptions(parser.WithAutoHeadingID()))

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
