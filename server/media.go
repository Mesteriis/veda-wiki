package main

import (
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"time"
)

func (w Wiki) media(rw http.ResponseWriter, r *http.Request) {
	rw.Header().Set("Cache-Control", "no-store")
	rw.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != "GET" {
		rw.WriteHeader(405)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/media/")
	parts := strings.SplitN(name, "/", 2)
	if len(parts) != 2 || !identifier.MatchString(parts[0]) || !safeRelative(parts[1]) {
		http.NotFound(rw, r)
		return
	}
	w.Store.mu.RLock()
	src, ok := w.Store.db.Sources[parts[0]]
	ref, present := src.Assets[parts[1]]
	visible := sourcePublicReadable(w.Store.db, src, time.Now())
	w.Store.mu.RUnlock()
	if _, e := hexKey(ref.SHA); e != nil {
		http.NotFound(rw, r)
		return
	}
	if !ok || !present || (w.Public && !visible) {
		http.NotFound(rw, r)
		return
	}
	file, e := os.Open(path.Join(w.Store.dir, "blobs", ref.SHA))
	if e != nil {
		http.NotFound(rw, r)
		return
	}
	defer file.Close()
	b, e := io.ReadAll(io.LimitReader(file, (4<<20)+1))
	if e != nil || len(b) > 4<<20 || digest(b) != ref.SHA {
		http.Error(rw, "Image unavailable", 503)
		return
	}
	rw.Header().Set("Content-Type", ref.MIME)
	rw.Header().Set("Cache-Control", "no-store")
	rw.Header().Set("X-Content-Type-Options", "nosniff")
	rw.Write(b)
}
