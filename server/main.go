package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net"
	"net/http"
	"path"
	"sort"
	"strings"
	"unicode"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"net/url"
)

//go:embed ui
var assets embed.FS
var markdown = goldmark.New(goldmark.WithExtensions(extension.GFM), goldmark.WithParserOptions(parser.WithAutoHeadingID()))

type Page struct {
	Path        string        `json:"path"`
	Title       string        `json:"title"`
	Environment string        `json:"environment"`
	SHA         string        `json:"sha256"`
	Text        string        `json:"-"`
	HTML        template.HTML `json:"-"`
}
type Snapshot struct {
	Generated string `json:"generated_at"`
	Pages     []Page `json:"pages"`
	Withheld  int    `json:"withheld"`
}
type Card struct {
	Page        Page
	Image       string
	Description string
	Tag         string
}
type Group struct {
	Name  string
	ID    string
	Count int
	Pages []Page
}
type View struct {
	Cards []Card

	Public    bool
	Groups    []Group
	Pages     []Page
	Page      *Page
	Query     string
	Selected  string
	Generated string
	Withheld  int
	Count     int
	TOC       template.HTML
}

type Wiki struct {
	AllowedHosts []string

	Public bool
	Store  *Store
	Token  string
}

func safePath(p string) bool {
	return p != "" && !strings.HasPrefix(p, "/") && path.Clean(p) == p && p != ".." && !strings.HasPrefix(p, "../") && strings.HasSuffix(p, ".md")
}
func headingID(id any) string {
	if b, ok := id.([]byte); ok {
		return string(b)
	}
	return fmt.Sprint(id)
}

type unicodeIDs struct{ used map[string]bool }

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
func (ids *unicodeIDs) Put(value []byte) { ids.used[string(value)] = true }
func render(p Page) (template.HTML, template.HTML, error) {
	source := []byte(p.Text)
	doc := markdown.Parser().Parse(text.NewReader(source), parser.WithContext(parser.NewContext(parser.WithIDs(&unicodeIDs{used: map[string]bool{}}))))
	var toc strings.Builder
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if link, ok := n.(*ast.Link); ok {
			u, e := url.Parse(string(link.Destination))
			if e == nil && u.Scheme == "" && u.Host == "" && u.Path != "" {
				target := path.Clean(path.Join(path.Dir(p.Path), u.Path))
				link.Destination = []byte("/?page=" + url.QueryEscape(target))
				if u.Fragment != "" {
					link.Destination = append(link.Destination, []byte("#"+url.PathEscape(u.Fragment))...)
				}
			}
		}
		if h, ok := n.(*ast.Heading); ok {
			if id, exists := h.AttributeString("id"); exists {
				fmt.Fprintf(&toc, "<a class=level%d href=\"#%s\">%s</a>", h.Level, template.HTMLEscapeString(headingID(id)), template.HTMLEscapeString(string(h.Text(source))))
			}
		}
		return ast.WalkContinue, nil
	})
	var out bytes.Buffer
	err := markdown.Renderer().Render(&out, source, doc)
	return template.HTML(out.String()), template.HTML(toc.String()), err
}
func (w Wiki) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	if len(w.AllowedHosts) > 0 {
		host := r.Host
		if parsed, _, err := net.SplitHostPort(host); err == nil {
			host = parsed
		}
		accepted := false
		for _, allowed := range w.AllowedHosts {
			if strings.EqualFold(host, allowed) {
				accepted = true
				break
			}
		}
		if !accepted {
			http.Error(rw, "Host not allowed", http.StatusForbidden)
			return
		}
	}

	if w.Store != nil {
		owner := w.authorized(r)
		if strings.HasPrefix(r.URL.Path, "/api/v1/") {
			w.api(rw, r, owner)
			return
		}
		if !owner {
			w.Public = true
		}
	}
	rw.Header().Set("Content-Security-Policy", "default-src 'none'; connect-src 'self'; style-src 'self'; font-src 'self'; script-src 'self'; img-src 'self' data:; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	rw.Header().Set("X-Content-Type-Options", "nosniff")
	rw.Header().Set("Cache-Control", "no-store")
	if r.Method != "GET" {
		http.Error(rw, "Method not allowed", 405)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/assets/") {
		subtree, _ := fs.Sub(assets, "ui")
		http.FileServer(http.FS(subtree)).ServeHTTP(rw, r)
		return
	}
	if r.URL.Path == "/" {
		index, e := assets.ReadFile("ui/index.html")
		if e != nil {
			http.Error(rw, "Frontend unavailable", 503)
			return
		}
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		rw.Write(index)
		return
	}
	s := w.Store.snapshot(w.Public)
	if r.URL.Path == "/health" {
		rw.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(rw, `{"status":"ok","pages":%d,"generated_at":%q}`, len(s.Pages), s.Generated)
		return
	}
	if r.URL.Path != "/api/state" {
		http.NotFound(rw, r)
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(query) > 300 {
		http.Error(rw, "Query too long", 400)
		return
	}
	env := r.URL.Query().Get("env")
	name := r.URL.Query().Get("page")
	v := View{Public: w.Public, Query: query, Selected: env, Generated: s.Generated, Withheld: s.Withheld, Count: len(s.Pages)}
	groups := map[string]*Group{}
	for _, p := range s.Pages {
		id := p.Environment
		if id == "" {
			id = "repository"
		}
		g := groups[id]
		if g == nil {
			g = &Group{Name: label(id), ID: id}
			groups[id] = g
		}
		g.Count++
		if true {
			g.Pages = append(g.Pages, p)
		}
		if name == p.Path {
			full, e := w.Store.page(p.Path, w.Public)
			if e != nil {
				http.Error(rw, "Page unavailable", 503)
				return
			}
			full.HTML, v.TOC, e = render(full)
			if e != nil {
				http.Error(rw, "Render unavailable", 500)
				return
			}
			v.Page = &full
		}
		if name == "" && (env == "" || env == "all" || env == id) {
			matched := query == "" || strings.Contains(strings.ToLower(p.Title+" "+p.Path), strings.ToLower(query))
			if !matched {
				if full, e := w.Store.page(p.Path, w.Public); e == nil {
					matched = strings.Contains(strings.ToLower(full.Text), strings.ToLower(query))
				}
			}
			if matched {
				v.Pages = append(v.Pages, p)
			}
		}
	}
	if name != "" && v.Page == nil {
		http.NotFound(rw, r)
		return
	}
	for i, p := range s.Pages {
		if i >= 4 {
			break
		}
		v.Cards = append(v.Cards, Card{Page: p, Image: []string{"veda-server.png", "veda-network.png", "veda-phone.png", "veda-travel.png"}[i], Description: "Документация", Tag: p.Environment})
	}
	for _, g := range groups {
		v.Groups = append(v.Groups, *g)
	}
	sort.Slice(v.Groups, func(i, j int) bool { return v.Groups[i].Name < v.Groups[j].Name })
	rw.Header().Set("Content-Type", "application/json; charset=utf-8")
	var body template.HTML
	if v.Page != nil {
		body = v.Page.HTML
	}
	json.NewEncoder(rw).Encode(map[string]any{"view": v, "html": body, "toc": v.TOC})
}
func label(id string) string { return id }
