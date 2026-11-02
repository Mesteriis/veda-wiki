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
	"time"
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
	TranslationOrigin string        `json:"translation_origin,omitempty"`
	OriginalSHA       string        `json:"original_sha256,omitempty"`
	Translated        bool          `json:"translated,omitempty"`
	Path              string        `json:"path"`
	Title             string        `json:"title"`
	Environment       string        `json:"environment"`
	SHA               string        `json:"sha256"`
	Updated           string        `json:"updated_at,omitempty"`
	Text              string        `json:"-"`
	HTML              template.HTML `json:"-"`
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
	ImageURL    string
}
type Group struct {
	Name  string
	ID    string
	Count int
	Pages []Page
}
type View struct {
	Cards []Card

	Public      bool
	Groups      []Group
	Pages       []Page
	Page        *Page
	Query       string
	Selected    string
	Generated   string
	Withheld    int
	Count       int
	GitHubCount int
	TOC         template.HTML
}

type Wiki struct {
	AllowedHosts []string

	Public            bool
	Store             *Store
	Token             string
	ReaderToken       string
	ReaderLocal       bool
	ReaderKeyRequired bool
	LocalImportSource string
	Sessions          *readerSessions
	AdminSessions     *readerSessions
	AccessSessions    *readerSessions
	Sync              *SyncManager
}

func safePath(p string) bool {
	return p != "" && !strings.HasPrefix(p, "/") && path.Clean(p) == p && p != ".." && !strings.HasPrefix(p, "../") && (strings.EqualFold(path.Ext(p), ".md") || strings.EqualFold(path.Ext(p), ".markdown"))
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

// Resolve repository paths independently of the source ID so a document
// cannot navigate into a different source using parent segments.
func sourceTarget(pagePath, destination string) (string, string, bool) {
	parts := strings.SplitN(pagePath, "/", 2)
	if len(parts) != 2 || !identifier.MatchString(parts[0]) || destination == "" {
		return "", "", false
	}
	var target string
	if strings.HasPrefix(destination, "/") {
		target = path.Clean(strings.TrimPrefix(destination, "/"))
	} else {
		target = path.Clean(path.Join(path.Dir(parts[1]), destination))
	}
	return parts[0], target, safeRelative(target)
}

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
				sourceID, target, valid := sourceTarget(p.Path, u.Path)
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
				sourceID, target, valid := sourceTarget(p.Path, u.Path)
				if valid {
					image.Destination = []byte("/media/" + sourceID + "/" + url.PathEscape(target))
				} else {
					image.Destination = []byte("")
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

	if strings.HasPrefix(r.URL.Path, "/api/session/") || strings.HasPrefix(r.URL.Path, "/api/admin/session/") || strings.HasPrefix(r.URL.Path, "/api/access/session/") {
		w.sessionAPI(rw, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/admin/github/") {
		w.adminGitHubAPI(rw, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/access/keys") || r.URL.Path == "/api/access/sources" {
		w.browserAccessAPI(rw, r)
		return
	}
	if w.ReaderLocal && w.LocalImportSource != "" && r.URL.Path == "/api/v1/import" && r.Method == "POST" && r.Context().Value(localRootKey{}) == true && r.Header.Get("Origin") == "" {
		var batch ImportBatch
		if decodeLimited(rw, r, &batch, 40<<20) != nil || batch.SourceID != w.LocalImportSource || batch.Complete {
			apiError(rw, ErrInvalid)
			return
		}
		result, err := w.Store.applyImport(batch, nil, true)
		if err != nil {
			apiError(rw, err)
			return
		}
		jsonResponse(rw, 200, result)
		return
	}
	if w.Store != nil {
		owner := w.authorized(r)
		implicitLocal := w.ReaderLocal && !w.ReaderKeyRequired && r.Header.Get("Authorization") == ""
		validation := r.Method == "POST" && r.URL.Path == "/api/v1/articles/translation-validate" && r.Header.Get("Origin") == ""
		reader := (w.readerAuthorized(r) || (!w.Public && (implicitLocal || (!validation && w.Sessions.valid(r))))) && (r.Method == "GET" || validation)
		if !w.Public && !owner && !reader && r.Method == "GET" {
			if hash, ok := w.Sessions.scopedCookie(r); ok {
				scoped, valid, err := w.Store.scopedViewHash(hash, time.Now())
				if err != nil {
					http.Error(rw, "Access unavailable", http.StatusServiceUnavailable)
					return
				}
				if valid {
					w.Store, reader = scoped, true
				}
			}
		}
		if !w.Public && !owner && strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			scoped, valid, err := w.Store.scopedView(token, time.Now())
			if err != nil {
				http.Error(rw, "Access unavailable", http.StatusServiceUnavailable)
				return
			}
			if valid {
				if r.Method != "GET" && !validation {
					http.Error(rw, "Read-only access", http.StatusForbidden)
					return
				}
				w.Store, reader = scoped, true
			}
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/") {
			w.api(rw, r, owner, reader)
			return
		}
		if !owner && !reader {
			w.Public = true
		}
	}
	if strings.HasPrefix(r.URL.Path, "/media/") {
		w.media(rw, r)
		return
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
	terms := searchTerms(query)
	scores := map[string]int{}
	w.Store.mu.RLock()
	for _, source := range w.Store.db.Sources {
		if source.ConnectionID != "" && (!w.Public || sourcePublic(w.Store.db, source, time.Now())) {
			v.GitHubCount++
		}
	}
	w.Store.mu.RUnlock()
	groups := map[string]*Group{}
	sources := map[string]Source{}
	for _, source := range w.Store.sources(w.Public) {
		sources[source.ID] = source.Source
	}
	for _, p := range s.Pages {
		id := p.Environment
		if id == "" {
			id = "repository"
		}
		g := groups[id]
		if g == nil {
			name := label(id)
			if source, ok := sources[id]; ok {
				name = source.Title
			}
			g = &Group{Name: name, ID: id}
			groups[id] = g
		}
		g.Count++
		g.Pages = append(g.Pages, p)
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
			score := 1
			if query != "" {
				body := ""
				if full, e := w.Store.page(p.Path, w.Public); e == nil {
					body = full.Text
				}
				score = searchScore(query, p.Title+" "+p.Path, body, terms)
			}
			if score > 0 {
				v.Pages = append(v.Pages, p)
				scores[p.Path] = score
			}
		}
	}
	if query != "" {
		sort.SliceStable(v.Pages, func(i, j int) bool { return scores[v.Pages[i].Path] > scores[v.Pages[j].Path] })
	}
	if name != "" && v.Page == nil {
		http.NotFound(rw, r)
		return
	}
	recentPages := append([]Page(nil), s.Pages...)
	sort.SliceStable(recentPages, func(i, j int) bool {
		a, _ := time.Parse(time.RFC3339Nano, recentPages[i].Updated)
		b, _ := time.Parse(time.RFC3339Nano, recentPages[j].Updated)
		if a.Equal(b) {
			return recentPages[i].Path < recentPages[j].Path
		}
		return a.After(b)
	})
	for i, p := range recentPages {
		if i >= 4 {
			break
		}
		card := Card{Page: p, Image: []string{"veda-server.png", "veda-network.png", "veda-phone.png", "veda-travel.png"}[i], Description: "Документация", Tag: p.Environment}
		if source := sources[p.Environment]; source.Metadata != nil {
			card.Description = source.Metadata.Description
			if source.Metadata.Image != "" {
				card.ImageURL = "/media/" + source.ID + "/" + url.PathEscape(source.Metadata.Image)
			}
		}
		v.Cards = append(v.Cards, card)
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
