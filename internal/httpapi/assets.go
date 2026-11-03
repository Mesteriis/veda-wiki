package httpapi

import (
	"net/http"
	"strings"

	"github.com/Mesteriis/veda-wiki/internal/webui"
)

// serveAssets serves the embedded interface: its static files and the entry page.
// It reports whether it answered the request.
func serveAssets(rw http.ResponseWriter, r *http.Request) bool {
	if strings.HasPrefix(r.URL.Path, "/assets/") {
		http.FileServer(http.FS(webui.Assets())).ServeHTTP(rw, r)
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
