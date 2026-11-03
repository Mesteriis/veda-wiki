package httpapi

import (
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// maxMediaBytes bounds an image served from the blob store.
const maxMediaBytes = 4 << 20

// media serves a source's image after checking its digest. The public
// listener serves only images of publicly readable sources.
func (h handler) media(rw http.ResponseWriter, r *http.Request) {
	rw.Header().Set("Cache-Control", "no-store")
	rw.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != "GET" {
		rw.WriteHeader(405)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/media/")
	parts := strings.SplitN(name, "/", 2)
	if len(parts) != 2 || !domain.ValidID(parts[0]) || !domain.SafeRelative(parts[1]) {
		http.NotFound(rw, r)
		return
	}
	var (
		ref                  domain.BlobRef
		ok, present, visible bool
	)
	_ = h.Store.View(func(db *domain.Database) error {
		var src domain.Source
		src, ok = db.Sources[parts[0]]
		ref, present = src.Assets[parts[1]]
		visible = domain.SourcePublicReadable(*db, src, time.Now())
		return nil
	})
	if _, err := domain.HexKey(ref.SHA); err != nil {
		http.NotFound(rw, r)
		return
	}
	if !ok || !present || (h.Public && !visible) {
		http.NotFound(rw, r)
		return
	}
	file, err := os.Open(path.Join(h.Store.Dir(), "blobs", ref.SHA))
	if err != nil {
		http.NotFound(rw, r)
		return
	}
	defer file.Close()
	b, err := io.ReadAll(io.LimitReader(file, maxMediaBytes+1))
	if err != nil || len(b) > maxMediaBytes || domain.Digest(b) != ref.SHA {
		http.Error(rw, "Image unavailable", http.StatusServiceUnavailable)
		return
	}
	rw.Header().Set("Content-Type", ref.MIME)
	rw.Header().Set("Cache-Control", "no-store")
	rw.Header().Set("X-Content-Type-Options", "nosniff")
	rw.Write(b)
}
