package main

import (
	"crypto/sha256"
	"embed"
	"encoding/json"
	"fmt"
	"github.com/Mesteriis/veda-wiki/internal/render"
	"github.com/Mesteriis/veda-wiki/internal/search"
	"html/template"
	"io/fs"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"net/url"
)

//go:embed ui
var assets embed.FS

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
	Name        string
	ID          string
	Count       int
	Pages       []Page
	Description string `json:",omitempty"`
	SourceURL   string `json:",omitempty"`
}
type SourceWarning struct {
	ID                   string
	Name                 string
	Partial              bool
	Dirty                bool
	Conflicts            int
	Withheld             *int
	SyncIssue            string `json:",omitempty"`
	LastImportObservedAt string `json:",omitempty"`
}
type View struct {
	GraphEdges     []render.Edge
	Cards          []Card
	SourceWarnings []SourceWarning

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

func (w Wiki) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	rw.Header().Set("Referrer-Policy", "no-referrer")
	if w.Store != nil {
		w.Store.RLock()
		invalid := w.Store.InvalidLocked()
		w.Store.RUnlock()
		if invalid {
			http.Error(rw, "Store unavailable", http.StatusServiceUnavailable)
			return
		}
	}
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
	if w.Store != nil && w.ReaderLocal && w.LocalImportSource != "" && r.URL.Path == "/api/local/bootstrap-admin" && r.Method == "POST" && r.Context().Value(localRootKey{}) == true && r.Header.Get("Origin") == "" {
		var in struct {
			ID string `json:"id"`
		}
		if decodeLimited(rw, r, &in, 1024) != nil {
			apiError(rw, ErrInvalid)
			return
		}
		token, err := w.Store.bootstrapAdmin(in.ID)
		if err != nil {
			apiError(rw, err)
			return
		}
		rw.Header().Set("Cache-Control", "no-store")
		jsonResponse(rw, 201, map[string]string{"setup_token": token})
		return
	}
	if w.Store != nil && w.ReaderLocal && w.LocalImportSource != "" && r.URL.Path == "/api/local/createsuperuser" && r.Method == "POST" && r.Context().Value(localRootKey{}) == true && r.Header.Get("Origin") == "" {
		var in struct {
			ID       string `json:"id"`
			Password string `json:"password"`
		}
		if decodeLimited(rw, r, &in, 2048) != nil {
			apiError(rw, ErrInvalid)
			return
		}
		if err := w.Store.createSuperuser(in.ID, in.Password); err != nil {
			apiError(rw, err)
			return
		}
		jsonResponse(rw, 201, map[string]bool{"created": true})
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/admin/github/") {
		w.adminGitHubAPI(rw, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/access/keys") || r.URL.Path == "/api/access/sources" || r.URL.Path == "/api/access/users" || r.URL.Path == "/api/access/teams" {
		w.browserAccessAPI(rw, r)
		return
	}
	if w.Store != nil && w.ReaderLocal && w.LocalImportSource != "" && r.URL.Path == "/api/local/remove-imported-source" && r.Method == "POST" && r.Context().Value(localRootKey{}) == true && r.Header.Get("Origin") == "" {
		var in struct {
			Generation string `json:"expected_generation"`
			Confirm    bool   `json:"confirm"`
		}
		if decodeLimited(rw, r, &in, 1024) != nil || !in.Confirm {
			apiError(rw, ErrInvalid)
			return
		}
		count, err := w.Store.removeLocalImportedSource(w.LocalImportSource, in.Generation)
		if err != nil {
			apiError(rw, err)
			return
		}
		jsonResponse(rw, 200, map[string]any{"source_id": w.LocalImportSource, "articles_removed": count})
		return
	}
	if w.ReaderLocal && w.LocalImportSource != "" && r.URL.Path == "/api/v1/import" && r.Method == "POST" && r.Context().Value(localRootKey{}) == true && r.Header.Get("Origin") == "" {
		var batch ImportBatch
		if decodeImport(rw, r, &batch) != nil || batch.SourceID != w.LocalImportSource || batch.Complete {
			apiError(rw, ErrInvalid)
			return
		}
		result, err := w.Store.applyImport(batch, nil, true)
		if err != nil {
			apiError(rw, err)
			return
		}
		if w.Sync != nil && (result.Generation != *batch.ExpectedImport || result.Conflicts > 0) && w.Store.hasEnabledMirror(batch.SourceID) {
			w.Sync.Wake()
		}
		jsonResponse(rw, 200, result)
		return
	}
	if w.Store != nil {
		owner := w.authorized(r)
		implicitLocal := w.ReaderLocal && !w.ReaderKeyRequired && r.Header.Get("Authorization") == ""
		validation := r.Method == "POST" && (r.URL.Path == "/api/v1/articles/translation-validate" || r.URL.Path == "/api/v1/articles/transfer-plan") && r.Header.Get("Origin") == ""
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
			if user, ok := w.Sessions.userCookie(r); ok && !reader {
				scoped, valid, err := w.Store.scopedViewUser(user.ID, user.AuthVersion)
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
		http.Error(rw, "Method not allowed", http.StatusMethodNotAllowed)
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
			http.Error(rw, "Frontend unavailable", http.StatusServiceUnavailable)
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
	// Resolve directory links only against the already authorized snapshot.
	// Exact page names win, then GitHub-style README, then index.
	if name != "" {
		visible := make(map[string]bool, len(s.Pages))
		for _, page := range s.Pages {
			visible[page.Path] = true
		}
		if !visible[name] {
			base := strings.TrimSuffix(name, "/")
			for _, suffix := range []string{"/README.md", "/index.md"} {
				if visible[base+suffix] {
					name = base + suffix
					break
				}
			}
		}
	}
	v := View{Public: w.Public, Query: query, Selected: env, Generated: s.Generated, Withheld: s.Withheld, Count: len(s.Pages)}
	if r.URL.Query().Get("graph") == "1" {
		v.GraphEdges = render.GraphEdges(s.Pages)
	}
	terms := search.Terms(query)
	scores := map[string]int{}
	w.Store.RLock()
	for _, source := range w.Store.DB().Sources {
		if source.ConnectionID != "" && (!w.Public || sourcePublicReadable(*w.Store.DB(), source, time.Now())) {
			v.GitHubCount++
		}
	}
	w.Store.RUnlock()
	groups := map[string]*Group{}
	sources := map[string]Source{}
	for _, source := range w.Store.sources(w.Public) {
		sources[source.ID] = source.Source
		if !w.Public && (source.ImportPartial || source.ImportDirty || source.UnresolvedConflicts > 0 || source.SyncIssue != "") {
			v.SourceWarnings = append(v.SourceWarnings, SourceWarning{ID: source.ID, Name: source.Title, Partial: source.ImportPartial, Dirty: source.ImportDirty, Conflicts: source.UnresolvedConflicts, Withheld: source.LastImportWithheld, SyncIssue: source.SyncIssue, LastImportObservedAt: source.LastImportObservedAt})
		}
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
			if source := sources[id]; source.Metadata != nil {
				g.Description = source.Metadata.Description
				g.SourceURL = source.Metadata.URL
			}
			groups[id] = g
		}
		g.Count++
		g.Pages = append(g.Pages, p)
		if name == p.Path {
			full, e := w.Store.page(p.Path, w.Public)
			if e != nil {
				http.Error(rw, "Page unavailable", http.StatusServiceUnavailable)
				return
			}
			full.HTML, v.TOC, e = render.Page(full)
			if e != nil {
				http.Error(rw, "Render unavailable", 500)
				return
			}
			v.Page = &full
		}
		if name == "" && (env == "" || env == "all" || env == id) {
			score := 1
			if query != "" {
				score = search.Revisions(query, p.Path, p.SearchSourceTitle, p.SearchOriginal, p.SearchDisplayed, terms)
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
	sort.Slice(v.Groups, func(i, j int) bool {
		if v.Groups[i].Name == v.Groups[j].Name {
			return v.Groups[i].ID < v.Groups[j].ID
		}
		return v.Groups[i].Name < v.Groups[j].Name
	})
	rw.Header().Set("Content-Type", "application/json; charset=utf-8")
	var body template.HTML
	if v.Page != nil {
		body = v.Page.HTML
	}
	payload, err := json.Marshal(map[string]any{"view": v, "html": body, "toc": v.TOC})
	if err != nil {
		http.Error(rw, "State unavailable", http.StatusInternalServerError)
		return
	}
	etag := fmt.Sprintf("\"%x\"", sha256.Sum256(payload))
	rw.Header().Set("ETag", etag)
	rw.Header().Add("Vary", "Cookie")
	rw.Header().Add("Vary", "Authorization")
	if r.Header.Get("If-None-Match") == etag {
		rw.WriteHeader(http.StatusNotModified)
		return
	}
	rw.Write(payload)
}
func label(id string) string { return id }
