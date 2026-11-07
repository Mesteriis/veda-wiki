package httpapi

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/Mesteriis/veda-wiki/internal/webui"
)

// pagePolicy is the Content-Security-Policy of every interface response.
// Its only frame is the diagram frame below.
const pagePolicy = "default-src 'none'; connect-src 'self'; style-src 'self'; font-src 'self'; script-src 'self'; " +
	"img-src 'self' data:; frame-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'"

// diagramFrame is the hidden page where the interface draws Mermaid
// diagrams. Mermaid sets inline styles while it lays a diagram out, so this
// page alone allows them; it still runs only the interface's own scripts,
// reaches no network and can be framed only by the interface itself.
const (
	diagramFrame       = "/diagram-frame.html"
	diagramFramePolicy = "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; font-src 'self'; " +
		"img-src 'self' data:; base-uri 'none'; form-action 'none'; frame-ancestors 'self'"
)

// serveAssets serves the embedded interface: its static files and the entry page.
// It reports whether it answered the request.
func serveAssets(rw http.ResponseWriter, r *http.Request) bool {
	if strings.HasPrefix(r.URL.Path, "/assets/") {
		serveStatic(rw, r)
		return true
	}
	if r.URL.Path == diagramFrame {
		page, err := fs.ReadFile(webui.Assets(), strings.TrimPrefix(diagramFrame, "/"))
		if err != nil {
			http.Error(rw, "Frontend unavailable", http.StatusServiceUnavailable)
			return true
		}
		rw.Header().Set("Content-Security-Policy", diagramFramePolicy)
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		rw.Write(page)
		return true
	}
	if r.URL.Path == "/" {
		index, err := webui.Index()
		if err != nil {
			http.Error(rw, "Frontend unavailable", http.StatusServiceUnavailable)
			return true
		}
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		rw.Write(index)
		return true
	}
	return false
}
