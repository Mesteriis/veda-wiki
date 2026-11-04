package httpapi

import (
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// maxMediaBytes bounds an image served from the blob store.
const maxMediaBytes = 4 << 20

// media serves a source's image after checking its digest. The public
// listener serves only images of publicly readable sources. With ?v=<sha256>
// it serves that digest only when it is the image of the path in a version of
// an article the reader may see (domain.RevisionImage): any stored version for
// the owner, the latest one for other private readers. Every refusal is the
// same 404, so a request cannot learn whether an image it may not see exists.
func (h handler) media(rw http.ResponseWriter, r *http.Request, owner bool) {
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
	query := r.URL.Query()
	_ = h.Store.View(func(db *domain.Database) error {
		if query.Has("v") {
			ref, present = domain.RevisionImage(*db, parts[0], parts[1], query.Get("v"), h.Public, owner && !h.Public, time.Now())
			ok, visible = present, present
			return nil
		}
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
	file, err := os.Open(h.Store.BlobPath(ref.SHA))
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
