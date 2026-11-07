package httpapi

import (
	"bytes"
	"compress/gzip"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/Mesteriis/veda-wiki/internal/webui"
)

// hashedAsset matches the file names the frontend build gives content
// hashes to ("main-Ca3BVUQl.js"). Only those never change under one name,
// so only they are cached for a year; other files keep no-store.
var hashedAsset = regexp.MustCompile(`-[A-Za-z0-9_-]{8}\.(js|css|svg|woff2?|png|jpe?g|webp|gif)$`)

// compressible lists the text types worth compressing.
var compressible = map[string]bool{".js": true, ".css": true, ".svg": true, ".json": true, ".html": true}

// gzipped holds the gzip form of embedded assets by path. The embedded files
// never change while the process runs, so each is compressed once.
var gzipped sync.Map // string -> []byte

// serveStatic serves one file of /assets/. Hashed files are cacheable for a
// year; text files go out gzip-compressed to clients that accept it.
func serveStatic(rw http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if hashedAsset.MatchString(name) {
		if _, err := fs.Stat(webui.Assets(), name); err == nil { // never cache a 404 for a year
			rw.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
	}
	ext := path.Ext(name)
	if !compressible[ext] || !acceptsGzip(r) {
		http.FileServer(http.FS(webui.Assets())).ServeHTTP(rw, r)
		return
	}
	body, ok := gzippedAsset(name)
	if !ok {
		http.FileServer(http.FS(webui.Assets())).ServeHTTP(rw, r)
		return
	}
	rw.Header().Set("Content-Type", mime.TypeByExtension(ext))
	rw.Header().Set("Content-Encoding", "gzip")
	rw.Header().Set("Content-Length", strconv.Itoa(len(body)))
	rw.Header().Add("Vary", "Accept-Encoding")
	rw.Write(body)
}

// gzippedAsset returns the compressed file, or false when it is missing.
func gzippedAsset(name string) ([]byte, bool) {
	if cached, ok := gzipped.Load(name); ok {
		return cached.([]byte), true
	}
	raw, err := fs.ReadFile(webui.Assets(), name)
	if err != nil {
		return nil, false
	}
	var out bytes.Buffer
	w, _ := gzip.NewWriterLevel(&out, gzip.BestCompression)
	w.Write(raw)
	w.Close()
	actual, _ := gzipped.LoadOrStore(name, out.Bytes())
	return actual.([]byte), true
}

// acceptsGzip reports an Accept-Encoding that lists gzip without q=0.
func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		coding, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if strings.EqualFold(strings.TrimSpace(coding), "gzip") {
			q := strings.ReplaceAll(strings.TrimSpace(params), " ", "")
			return q != "q=0" && q != "q=0.0" && q != "q=0.00" && q != "q=0.000"
		}
	}
	return false
}
