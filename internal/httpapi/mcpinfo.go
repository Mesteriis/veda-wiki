package httpapi

import (
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/auth"
	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/mcp"
)

// ClientHeader is the label veda mcp puts on its requests: "mcp/reader",
// "mcp/editor" or "mcp/owner"; the /mcp endpoint labels its own as "mcp/remote". It is a label, never authorization: a mode
// is recorded only when the request's own credential matches it.
const ClientHeader = "X-Veda-Client"

// ClientActivity remembers when each MCP mode last reached the server. It is
// process-local and shared by every listener of one process.
type ClientActivity struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func (a *ClientActivity) seen(mode string, at time.Time) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.last == nil {
		a.last = map[string]time.Time{}
	}
	a.last[mode] = at
}

func (a *ClientActivity) snapshot() map[string]time.Time {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make(map[string]time.Time, len(a.last))
	for mode, at := range a.last {
		out[mode] = at
	}
	return out
}

// mcpMode is the MCP mode of a request whose label agrees with the role it
// actually resolved to, or "" for any other client.
func mcpMode(r *http.Request, owner, reader bool) string {
	switch r.Header.Get(ClientHeader) {
	case "mcp/editor":
		if owner {
			return "editor"
		}
	case "mcp/owner":
		if owner {
			return "owner"
		}
	case "mcp/reader":
		if reader && !owner {
			return "reader"
		}
	case "mcp/remote":
		if reader && !owner {
			return "remote"
		}
	}
	return ""
}

// writeOrigin is the Via recorded on a revision the owner writes.
func writeOrigin(r *http.Request) string {
	switch mcpMode(r, true, false) {
	case "editor":
		return "mcp-editor"
	case "owner":
		return "mcp-owner"
	}
	return "rest"
}

// adminMCPAPI serves the MCP page to a signed-in administrator.
func (h handler) adminMCPAPI(rw http.ResponseWriter, r *http.Request) {
	if h.Public || h.Store == nil || !h.adminCookieValid(r, h.AdminSessions, auth.AdminCookie) {
		http.NotFound(rw, r)
		return
	}
	request := r.Clone(r.Context())
	request.URL.Path = strings.Replace(r.URL.Path, "/api/admin/mcp/", "/api/v1/mcp/", 1)
	h.mcpAPI(rw, request)
}

type mcpTool struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Modes       []string `json:"modes"`
}

type mcpClientSeen struct {
	Mode     string `json:"mode"`
	LastSeen string `json:"last_seen"`
}

type mcpWrite struct {
	SourceID string `json:"source_id"`
	Path     string `json:"path"`
	Revision int    `json:"revision"`
	Via      string `json:"via"`
	Created  string `json:"created_at"`
}

// mcpAPI answers the owner's read-only MCP routes. The caller has checked
// that the request comes from the owner.
func (h handler) mcpAPI(rw http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		rw.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	switch r.URL.Path {
	case "/api/v1/mcp/tools":
		tools := []mcpTool{}
		for _, spec := range mcp.Tools {
			modes := []string{"owner"}
			if spec.ReadOnly || spec.Editor {
				modes = append([]string{"editor"}, modes...)
			}
			if spec.ReadOnly {
				modes = append([]string{"reader"}, modes...)
			}
			tools = append(tools, mcpTool{Name: spec.Name, Description: spec.Description, Modes: modes})
		}
		jsonResponse(rw, 200, map[string]any{"tools": tools})
	case "/api/v1/mcp/activity":
		clients := []mcpClientSeen{}
		if h.Activity != nil {
			for mode, at := range h.Activity.snapshot() {
				clients = append(clients, mcpClientSeen{Mode: mode, LastSeen: at.UTC().Format(time.RFC3339Nano)})
			}
		}
		sort.Slice(clients, func(i, j int) bool { return clients[i].Mode < clients[j].Mode })
		jsonResponse(rw, 200, map[string]any{"clients": clients, "writes": h.Store.mcpWrites(20)})
	default:
		http.NotFound(rw, r)
	}
}

// mcpWrites returns the newest revisions written through MCP, at most limit.
func (s *dataStore) mcpWrites(limit int) []mcpWrite {
	writes := []mcpWrite{}
	_ = s.View(func(db *domain.Database) error {
		for _, a := range db.Articles {
			if a.Deleted {
				continue
			}
			for _, rev := range a.Versions {
				if strings.HasPrefix(rev.Via, "mcp-") {
					writes = append(writes, mcpWrite{SourceID: a.SourceID, Path: a.Path, Revision: rev.Number, Via: rev.Via, Created: rev.Created})
				}
			}
		}
		return nil
	})
	sort.Slice(writes, func(i, j int) bool { return writes[i].Created > writes[j].Created })
	if len(writes) > limit {
		writes = writes[:limit]
	}
	return writes
}
