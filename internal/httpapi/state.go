package httpapi

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/render"
	"github.com/Mesteriis/veda-wiki/internal/search"
)

// Card is a recently updated page on the home view.
type Card struct {
	Page        domain.Page
	Image       string
	Description string
	Tag         string
	ImageURL    string
}

// Group lists the pages of one source.
type Group struct {
	Name        string
	ID          string
	Count       int
	Pages       []domain.Page
	Description string `json:",omitempty"`
	SourceURL   string `json:",omitempty"`
}

// SourceWarning tells the owner that a source needs attention.
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

// View is the reading interface state; its field names are the JSON keys.
type View struct {
	GraphEdges     []render.Edge
	Cards          []Card
	SourceWarnings []SourceWarning

	Public      bool
	Groups      []Group
	Pages       []domain.Page
	Page        *domain.Page
	Query       string
	Selected    string
	Generated   string
	Withheld    int
	Count       int
	GitHubCount int
	TOC         template.HTML
}

// state answers /health and /api/state from the snapshot the caller may see.
// A page is resolved only against that snapshot, so a directory link never
// reveals another source. The response carries an ETag that varies with the
// credentials.
func (h handler) state(rw http.ResponseWriter, r *http.Request) {
	s := h.Store.snapshot(h.Public)
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
	v := View{Public: h.Public, Query: query, Selected: env, Generated: s.Generated, Withheld: s.Withheld, Count: len(s.Pages)}
	if r.URL.Query().Get("graph") == "1" {
		v.GraphEdges = render.GraphEdges(s.Pages)
	}
	terms := search.Terms(query)
	scores := map[string]int{}
	_ = h.Store.View(func(db *domain.Database) error {
		for _, source := range db.Sources {
			if source.ConnectionID != "" && (!h.Public || domain.SourcePublicReadable(*db, source, time.Now())) {
				v.GitHubCount++
			}
		}
		return nil
	})
	groups := map[string]*Group{}
	sources := map[string]domain.Source{}
	for _, source := range h.Store.sources(h.Public) {
		sources[source.ID] = source.Source
		if !h.Public && (source.ImportPartial || source.ImportDirty || source.UnresolvedConflicts > 0 || source.SyncIssue != "") {
			v.SourceWarnings = append(v.SourceWarnings, SourceWarning{
				ID:                   source.ID,
				Name:                 source.Title,
				Partial:              source.ImportPartial,
				Dirty:                source.ImportDirty,
				Conflicts:            source.UnresolvedConflicts,
				Withheld:             source.LastImportWithheld,
				SyncIssue:            source.SyncIssue,
				LastImportObservedAt: source.LastImportObservedAt,
			})
		}
	}
	for _, p := range s.Pages {
		id := p.Environment
		if id == "" {
			id = "repository"
		}
		g := groups[id]
		if g == nil {
			name := id
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
			full, e := h.Store.page(p.Path, h.Public)
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
				score = search.Article(query, p.Path, p.SearchSourceTitle, p.SearchText, terms)
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
	recentPages := append([]domain.Page(nil), s.Pages...)
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
