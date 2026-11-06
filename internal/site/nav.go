package site

import (
	"path"
	"sort"
	"strings"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// entry is one page in the site navigation.
type entry struct {
	Title  string
	target string // HTML path from the site root
	Depth  int
}

// NavItem is an entry as seen from one page.
type NavItem struct {
	Title   string
	Href    string
	Depth   int
	Current bool
}

type nav []entry

// navigation lists every page: directories in path order, each directory's
// index first, then its pages by title.
func navigation(files, outputs map[string]string) nav {
	var items nav
	for name, target := range outputs {
		dir := path.Dir(target)
		depth := 0
		if dir != "." {
			depth = strings.Count(dir, "/") + 1
		}
		if path.Base(target) == "index.html" && depth > 0 {
			depth--
		}
		items = append(items, entry{Title: domain.Title(files[name], name), target: target, Depth: depth})
	}
	key := func(e entry) (string, int, string) {
		dir, index := path.Dir(e.target), 1
		if path.Base(e.target) == "index.html" {
			index = 0
		}
		return dir, index, strings.ToLower(e.Title)
	}
	sort.Slice(items, func(i, j int) bool {
		di, ii, ti := key(items[i])
		dj, ij, tj := key(items[j])
		if di != dj {
			return di < dj
		}
		if ii != ij {
			return ii < ij
		}
		return ti < tj
	})
	return items
}

// forPage returns the navigation with links relative to the page target.
func (n nav) forPage(target string) []NavItem {
	out := make([]NavItem, 0, len(n))
	for _, e := range n {
		out = append(out, NavItem{Title: e.Title, Href: relative(target, e.target), Depth: e.Depth, Current: e.target == target})
	}
	return out
}
