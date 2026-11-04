package httpapi

import (
	"bytes"
	"github.com/Mesteriis/veda-wiki/internal/render"
	"github.com/Mesteriis/veda-wiki/internal/store"
	"image"
	"image/color"
	"image/png"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSameMediaPathsRemainSourceScopedAfterSQLiteRestart(t *testing.T) {
	s := testStore(t)
	makeImage := func(c color.RGBA) []byte {
		img := image.NewRGBA(image.Rect(0, 0, 2, 2))
		img.SetRGBA(0, 0, c)
		var out bytes.Buffer
		if err := png.Encode(&out, img); err != nil {
			t.Fatal(err)
		}
		return out.Bytes()
	}
	images := map[string][]byte{"alpha": makeImage(color.RGBA{R: 255, A: 255}), "beta": makeImage(color.RGBA{B: 255, A: 255})}
	for _, id := range []string{"alpha", "beta"} {
		batch := testBatch()
		batch.SourceID = id
		batch.Metadata.Title = id
		batch.Files = map[string]string{"docs/guide.md": "# Guide\n\n![Icon](img/icon.png)\n\n[Other](other.md)", "docs/other.md": "# Other"}
		batch.Assets = map[string][]byte{"docs/img/icon.png": images[id]}
		if id == "beta" {
			batch.Visibility = "public"
		}
		if _, err := s.importBatch(batch); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.MigrateSQLite(s.Dir()); err != nil {
		t.Fatal(err)
	}
	s, err := openStore(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	key, err := s.auth().CreateAccessKey("agent", []string{"alpha"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"alpha", "beta"} {
		p, err := s.page(id+"/docs/guide.md", false)
		if err != nil {
			t.Fatal(err)
		}
		html, _, err := render.Page(p)
		if err != nil || !strings.Contains(string(html), "/media/"+id+"/docs%2Fimg%2Ficon.png") || !strings.Contains(string(html), "page="+id+"%2Fdocs%2Fother.md") {
			t.Fatal("relative links lost source identity", id, err)
		}
		response := request(Wiki{Store: s, Token: "test-owner"}, "GET", "/media/"+id+"/docs/img/icon.png", "", true)
		if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), images[id]) {
			t.Fatal("wrong source image", id, response.Code)
		}
	}
	images["beta"] = makeImage(color.RGBA{G: 255, A: 255})
	updated := testBatch()
	updated.SourceID, updated.Metadata.Title = "beta", "beta"
	updated.Files = map[string]string{"docs/guide.md": "# Guide\n\n![Icon](img/icon.png)\n\n[Other](other.md)", "docs/other.md": "# Other"}
	updated.Assets = map[string][]byte{"docs/img/icon.png": images["beta"]}
	updated.Commit = strings.Repeat("b", 40)
	updated = applyAgain(s, updated)
	if _, err = s.importBatch(updated); err != nil {
		t.Fatal(err)
	}
	s, err = openStore(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"alpha", "beta"} {
		response := request(Wiki{Store: s, Token: "test-owner"}, "GET", "/media/"+id+"/docs/img/icon.png", "", true)
		if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), images[id]) {
			t.Fatal("source image changed incorrectly after import", id, response.Code)
		}
	}
	public := Wiki{Store: s, Public: true}
	if request(public, "GET", "/media/alpha/docs/img/icon.png", "", false).Code != 404 || request(public, "GET", "/media/beta/docs/img/icon.png", "", false).Code != 200 {
		t.Fatal("public media scope escaped")
	}
	for _, tc := range []struct {
		id     string
		status int
	}{{"alpha", 200}, {"beta", 404}} {
		r := httptest.NewRequest("GET", "/media/"+tc.id+"/docs/img/icon.png", nil)
		r.Header.Set("Authorization", "Bearer "+key)
		out := httptest.NewRecorder()
		(Wiki{Store: s, Token: "test-owner"}).ServeHTTP(out, r)
		if out.Code != tc.status {
			t.Fatal("scoped media escaped", tc.id, out.Code)
		}
	}
}
