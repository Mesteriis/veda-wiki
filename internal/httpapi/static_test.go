package httpapi

import (
	"bytes"
	"compress/gzip"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/webui"
)

// firstAsset returns an embedded asset whose name matches keep.
func firstAsset(t *testing.T, keep func(string) bool) string {
	t.Helper()
	entries, err := fs.ReadDir(webui.Assets(), "assets")
	if err != nil {
		t.Skip("frontend not built")
	}
	for _, e := range entries {
		if !e.IsDir() && keep(e.Name()) {
			return "assets/" + e.Name()
		}
	}
	t.Skip("no matching asset in this build")
	return ""
}

func TestHashedScriptsAreCachedAndCompressed(t *testing.T) {
	name := firstAsset(t, func(n string) bool { return strings.HasSuffix(n, ".js") && hashedAsset.MatchString(n) })
	raw, err := fs.ReadFile(webui.Assets(), name)
	if err != nil {
		t.Fatal(err)
	}
	h := New(Config{}, Deps{})
	r := httptest.NewRequest(http.MethodGet, "/"+name, nil)
	r.Header.Set("Accept-Encoding", "br, gzip;q=0.8")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != 200 || rec.Header().Get("Content-Encoding") != "gzip" || rec.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" ||
		!strings.Contains(rec.Header().Get("Vary"), "Accept-Encoding") || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/javascript") {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := io.ReadAll(zr); !bytes.Equal(got, raw) {
		t.Fatal("compressed body differs from the asset")
	}
	// Without gzip, or with gzip refused, the file goes out as is.
	for _, accept := range []string{"", "gzip;q=0", "br"} {
		r := httptest.NewRequest(http.MethodGet, "/"+name, nil)
		r.Header.Set("Accept-Encoding", accept)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Header().Get("Content-Encoding") != "" || !bytes.Equal(rec.Body.Bytes(), raw) {
			t.Fatalf("%q: %v", accept, rec.Header())
		}
	}
}

func TestUnhashedAssetsAreNotCachedForLong(t *testing.T) {
	name := firstAsset(t, func(n string) bool { return !hashedAsset.MatchString(n) })
	rec := httptest.NewRecorder()
	New(Config{}, Deps{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/"+name, nil))
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%s: %d %q", name, rec.Code, rec.Header().Get("Cache-Control"))
	}
	missing := httptest.NewRequest(http.MethodGet, "/assets/missing-AAAAAAAA.js", nil)
	missing.Header.Set("Accept-Encoding", "gzip")
	rec = httptest.NewRecorder()
	New(Config{}, Deps{}).ServeHTTP(rec, missing)
	if rec.Code != 404 || rec.Header().Get("Content-Encoding") != "" || strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("missing asset: %d %v", rec.Code, rec.Header())
	}
}
