package httpapi

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
	"github.com/Mesteriis/veda-wiki/internal/render"
	"github.com/Mesteriis/veda-wiki/internal/transfer"
)

func colorPNG(t *testing.T, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.SetRGBA(0, 0, c)
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

const guideKey = "handbook/docs/guide.md"

func TestMediaServesTheImagesOfTheShownVersion(t *testing.T) {
	s := testStore(t)
	oldImage, newImage, privateImage := colorPNG(t, color.RGBA{R: 255, A: 255}), colorPNG(t, color.RGBA{B: 255, A: 255}), colorPNG(t, color.RGBA{G: 255, A: 255})
	oldSHA, newSHA, privateSHA := domain.Digest(oldImage), domain.Digest(newImage), domain.Digest(privateImage)
	text := "# Guide\n\n![Icon](img/icon.png)"
	f := githubtest.New(t, map[string]string{"docs/guide.md": text, "docs/img/icon.png": string(oldImage)})
	m := connectFixture(t, s, f, false)
	a := s.state(t).Articles[guideKey]
	if first, _ := domain.LastRevision(a); first.Assets["docs/img/icon.png"].SHA != oldSHA || a.Published == nil || a.Published.Assets["docs/img/icon.png"].SHA != oldSHA {
		t.Fatal("imported revision or publication lacks its image")
	}
	f.Advance(map[string]string{"docs/guide.md": text + "\n\nChanged", "docs/img/icon.png": string(newImage)})
	syncFixtureSource(t, m, "handbook")
	a = s.state(t).Articles[guideKey]
	first, _ := domain.RevisionByNumber(a, 1)
	second, _ := domain.RevisionByNumber(a, 2)
	if first.Assets["docs/img/icon.png"].SHA != oldSHA || second.Assets["docs/img/icon.png"].SHA != newSHA {
		t.Fatal("revisions do not keep the images of their own time")
	}
	// The public listener keeps showing the first revision until it is
	// published again; the second one stays unpublished.
	s.mutate(t, func(db *domain.Database) {
		a := db.Articles[guideKey]
		published, _ := domain.RevisionByNumber(a, 1)
		published.Number = 0
		a.Published, a.PublishedLocal = &published, true
		db.Articles[guideKey] = a
	})
	alpha := testBatch()
	alpha.SourceID, alpha.Metadata.Title = "alpha", "alpha"
	alpha.Files = map[string]string{"docs/guide.md": text}
	alpha.Assets = map[string][]byte{"docs/img/icon.png": privateImage}
	if _, err := s.importBatch(alpha); err != nil {
		t.Fatal(err)
	}
	page, err := s.page(guideKey, true)
	if err != nil {
		t.Fatal(err)
	}
	html, _, err := render.Page(page)
	if err != nil || !strings.Contains(string(html), "/media/handbook/docs%2Fimg%2Ficon.png?v="+oldSHA) {
		t.Fatal("public page does not name the published image", err)
	}
	public, owner := Wiki{Store: s, Public: true}, Wiki{Store: s, Token: "test-owner"}
	media := func(source, v string) string { return "/media/" + source + "/docs/img/icon.png?v=" + v }
	unknown := request(public, "GET", media("handbook", strings.Repeat("0", 64)), "", false)
	if unknown.Code != 404 {
		t.Fatal("unknown digest served", unknown.Code)
	}
	for _, tc := range []struct {
		name   string
		w      Wiki
		url    string
		status int
		body   []byte
	}{
		{"public old published image", public, media("handbook", oldSHA), 200, oldImage},
		{"public unpublished revision image", public, media("handbook", newSHA), 404, nil},
		{"public private source image", public, media("alpha", privateSHA), 404, nil},
		{"public owner token is ignored", Wiki{Store: s, Public: true, Token: "test-owner"}, media("alpha", privateSHA), 404, nil},
		{"public digest of another source", public, media("alpha", oldSHA), 404, nil},
		{"private stored old revision image", owner, media("handbook", oldSHA), 200, oldImage},
		{"private latest revision image", owner, media("handbook", newSHA), 200, newImage},
		{"private source image", owner, media("alpha", privateSHA), 200, privateImage},
		{"uppercase digest", owner, media("handbook", strings.ToUpper(oldSHA)), 404, nil},
		{"short digest", owner, media("handbook", oldSHA[:63]), 404, nil},
		{"non-hex digest", owner, media("handbook", strings.Repeat("g", 64)), 404, nil},
		{"empty digest", owner, media("handbook", ""), 404, nil},
		{"public without digest serves the source image", public, "/media/handbook/docs/img/icon.png", 200, newImage},
	} {
		auth := tc.w.Token != ""
		r := request(tc.w, "GET", tc.url, "", auth)
		if r.Code != tc.status || (tc.body != nil && !bytes.Equal(r.Body.Bytes(), tc.body)) {
			t.Error(tc.name, r.Code)
		}
		if r.Header().Get("Cache-Control") != "no-store" || r.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Error(tc.name, "missing media headers")
		}
		if tc.status == 404 && r.Body.String() != unknown.Body.String() {
			t.Error(tc.name, "404 differs from an unknown digest")
		}
	}
	// Readers cannot read revision history, so on the private listener a
	// reader key or a scoped key loads only the latest revision's images.
	scopedKey, err := s.auth().CreateAccessKey("agent", []string{"handbook"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	private := Wiki{Store: s, Token: "test-owner", ReaderToken: "test-reader"}
	for _, credential := range []string{"test-reader", scopedKey} {
		get := func(url string) *httptest.ResponseRecorder {
			r := httptest.NewRequest("GET", url, nil)
			r.Header.Set("Authorization", "Bearer "+credential)
			out := httptest.NewRecorder()
			private.ServeHTTP(out, r)
			return out
		}
		readerUnknown := get(media("handbook", strings.Repeat("0", 64)))
		if older := get(media("handbook", oldSHA)); older.Code != 404 || older.Body.String() != readerUnknown.Body.String() {
			t.Error("reader loaded an older revision's image", older.Code)
		}
		if latest := get(media("handbook", newSHA)); latest.Code != 200 || !bytes.Equal(latest.Body.Bytes(), newImage) {
			t.Error("reader cannot load the latest revision's image", latest.Code)
		}
	}
	if older := request(private, "GET", media("handbook", oldSHA), "", true); older.Code != 200 || !bytes.Equal(older.Body.Bytes(), oldImage) {
		t.Error("owner cannot load an older revision's image", older.Code)
	}
}

func TestUnchangedTextFollowsTheImportedImage(t *testing.T) {
	s := testStore(t)
	oldImage, newImage := colorPNG(t, color.RGBA{R: 255, A: 255}), colorPNG(t, color.RGBA{B: 255, A: 255})
	text := "# Guide\n\n![Icon](img/icon.png)"
	f := githubtest.New(t, map[string]string{"docs/guide.md": text, "docs/img/icon.png": string(oldImage)})
	m := connectFixture(t, s, f, false)
	f.Advance(map[string]string{"docs/guide.md": text, "docs/img/icon.png": string(newImage)})
	syncFixtureSource(t, m, "handbook")
	a := s.state(t).Articles[guideKey]
	last, _ := domain.LastRevision(a)
	if domain.LatestRevision(a) != 1 || last.Assets["docs/img/icon.png"].SHA != domain.Digest(newImage) || a.Published.Assets["docs/img/icon.png"].SHA != domain.Digest(newImage) {
		t.Fatal("upstream image change not shown for unchanged text")
	}
}

func TestRevisionWritersBindImages(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	source(t, s, "to", "private")
	ref := fixtureAsset(t, s, "from", "img/icon.png")
	if _, err := s.put("from", "a.md", "# A\n\n![icon](img/icon.png)\n\n[Other](other.md)", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("from", "other.md", "# Other\n\n![icon](img/icon.png)", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.copyArticle(transfer.CopyRequest{SourceID: "from", SourcePath: "other.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "nested/other.md"}); err != nil {
		t.Fatal(err)
	}
	source(t, s, "graph", "private")
	if _, err := s.copyArticle(transfer.CopyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "graph", DestinationPath: "a.md", IncludeLinked: true}); err != nil {
		t.Fatal(err)
	}
	db := s.state(t)
	for key, image := range map[string]string{"from/a.md": "img/icon.png", "to/nested/other.md": "nested/img/icon.png", "graph/a.md": "img/icon.png", "graph/other.md": "img/icon.png"} {
		last, _ := domain.LastRevision(db.Articles[key])
		if last.Assets[image] != ref || len(last.Assets) != 1 {
			t.Error("revision lacks its image", key, last.Assets)
		}
	}

	b := testBatch()
	b.Files = map[string]string{"docs/guide.md": "# One\n\n![I](i.png)"}
	b.Assets = map[string][]byte{"docs/i.png": smallPNG(t)}
	if _, err := s.importBatch(b); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("handbook", "docs/guide.md", "# Local\n\n![I](i.png)", 1); err != nil {
		t.Fatal(err)
	}
	b = applyAgain(s, b)
	b.Files = map[string]string{"docs/guide.md": "# Upstream\n\n![I](i.png)"}
	b.Commit = strings.Repeat("b", 40)
	if _, err := s.importBatch(b); err != nil {
		t.Fatal(err)
	}
	if err := s.resolveConflict("handbook", "docs/guide.md", 0, 2, "upstream"); err != nil {
		t.Fatal(err)
	}
	a := s.state(t).Articles["handbook/docs/guide.md"]
	for _, n := range []int{1, 2, 3} {
		rev, _ := domain.RevisionByNumber(a, n)
		if rev.Assets["docs/i.png"].SHA != domain.Digest(smallPNG(t)) {
			t.Error("revision lacks its image", n)
		}
	}
}

func TestScopedKeyCannotLoadRevisionImagesOutsideItsSources(t *testing.T) {
	s := testStore(t)
	images := map[string][]byte{"alpha": colorPNG(t, color.RGBA{R: 255, A: 255}), "beta": colorPNG(t, color.RGBA{B: 255, A: 255})}
	for _, id := range []string{"alpha", "beta"} {
		b := testBatch()
		b.SourceID, b.Metadata.Title = id, id
		b.Files = map[string]string{"docs/guide.md": "# Guide\n\n![Icon](img/icon.png)"}
		b.Assets = map[string][]byte{"docs/img/icon.png": images[id]}
		if _, err := s.importBatch(b); err != nil {
			t.Fatal(err)
		}
	}
	key, err := s.auth().CreateAccessKey("agent", []string{"alpha"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	get := func(url string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", url, nil)
		r.Header.Set("Authorization", "Bearer "+key)
		out := httptest.NewRecorder()
		(Wiki{Store: s, Token: "test-owner"}).ServeHTTP(out, r)
		return out
	}
	granted := get("/media/alpha/docs/img/icon.png?v=" + domain.Digest(images["alpha"]))
	if granted.Code != 200 || !bytes.Equal(granted.Body.Bytes(), images["alpha"]) {
		t.Fatal("granted revision image unavailable", granted.Code)
	}
	unknown := get("/media/alpha/docs/img/icon.png?v=" + strings.Repeat("0", 64))
	denied := get("/media/beta/docs/img/icon.png?v=" + domain.Digest(images["beta"]))
	if unknown.Code != 404 || denied.Code != 404 || denied.Body.String() != unknown.Body.String() {
		t.Fatal("scoped key reached another source's revision image", denied.Code)
	}
}

func TestImportKeepsImagesOfLocallyChangedRevision(t *testing.T) {
	s := testStore(t)
	oldImage, newImage := colorPNG(t, color.RGBA{R: 255, A: 255}), colorPNG(t, color.RGBA{B: 255, A: 255})
	b := testBatch()
	b.Files = map[string]string{"docs/guide.md": "# Guide\n\n![Icon](img/icon.png)"}
	b.Assets = map[string][]byte{"docs/img/icon.png": oldImage}
	if _, err := s.importBatch(b); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("handbook", "docs/guide.md", "# Local\n\n![Icon](img/icon.png)", 1); err != nil {
		t.Fatal(err)
	}
	b = applyAgain(s, b)
	b.Assets = map[string][]byte{"docs/img/icon.png": newImage}
	b.Commit = strings.Repeat("b", 40)
	if _, err := s.importBatch(b); err != nil {
		t.Fatal(err)
	}
	a := s.state(t).Articles["handbook/docs/guide.md"]
	last, _ := domain.LastRevision(a)
	if domain.LatestRevision(a) != 2 || last.Assets["docs/img/icon.png"].SHA != domain.Digest(oldImage) {
		t.Fatal("import rebound the images of a local revision", last.Assets)
	}
}

func TestPublicListenerServesLocalPublicLatestRevisionImage(t *testing.T) {
	s := testStore(t)
	image := colorPNG(t, color.RGBA{G: 255, A: 255})
	b := testBatch()
	b.Visibility = "public"
	b.Files = map[string]string{"docs/guide.md": "# Guide\n\n![Icon](img/icon.png)"}
	b.Assets = map[string][]byte{"docs/img/icon.png": image}
	if _, err := s.importBatch(b); err != nil {
		t.Fatal(err)
	}
	r := request(Wiki{Store: s, Public: true}, "GET", "/media/handbook/docs/img/icon.png?v="+domain.Digest(image), "", false)
	if r.Code != 200 || !bytes.Equal(r.Body.Bytes(), image) {
		t.Fatal("public local revision image unavailable", r.Code)
	}
}
