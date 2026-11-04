package domain

import (
	"strings"
	"testing"
	"time"
)

func assetRef(c string) BlobRef { return BlobRef{SHA: strings.Repeat(c, 64), MIME: "image/png"} }

func TestRevisionAssetsKeepsOnlyReferencedImages(t *testing.T) {
	src := Source{ID: "one", Assets: map[string]BlobRef{"docs/img/a.png": assetRef("a"), "docs/img/b.png": assetRef("b"), "img/root.png": assetRef("c")}}
	got := RevisionAssets("# A\n\n![A](img/a.png)\n\n![Root](/img/root.png)\n\n![Missing](img/missing.png)\n\n![Remote](https://example.com/b.png)", "docs/page.md", src)
	if len(got) != 2 || got["docs/img/a.png"] != assetRef("a") || got["img/root.png"] != assetRef("c") {
		t.Fatal("unexpected revision assets", got)
	}
	if got := RevisionAssets("# No images", "docs/page.md", src); got != nil {
		t.Fatal("text without images has assets", got)
	}
	if got := RevisionAssets("![A](img/a.png)", "docs/page.md", Source{}); got != nil {
		t.Fatal("source without assets produced assets", got)
	}
}

func TestPutArticleBindsReferencedImages(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	db := Database{Sources: map[string]Source{"one": {ID: "one", Title: "One", Kind: "wiki", Visibility: "public", Assets: map[string]BlobRef{"img/a.png": assetRef("a"), "img/b.png": assetRef("b")}}}, Articles: map[string]Article{}}
	rev, _, err := PutArticle(&db, "one", "a.md", "# A\n\n![A](img/a.png)", 0, now)
	if err != nil || len(rev.Assets) != 1 || rev.Assets["img/a.png"] != assetRef("a") {
		t.Fatal("written revision lacks its images", rev.Assets, err)
	}
	a := db.Articles["one/a.md"]
	if stored, _ := LastRevision(a); stored.Assets["img/a.png"] != assetRef("a") {
		t.Fatal("stored revision lacks its images")
	}
	if a.Published == nil || a.Published.Assets["img/a.png"] != assetRef("a") || len(a.Published.Assets) != 1 {
		t.Fatal("publication copy lacks its images")
	}
}

// publicConnected returns a database with a verified public GitHub source
// "pub" and a private local source "priv", each with one image article.
func publicConnected(now time.Time) Database {
	commit := strings.Repeat("1", 40)
	conn := GitHubConnection{ID: "pub", Verification: "verified", VerifiedAt: now.Format(time.RFC3339Nano), LastPull: commit, TreeSHA: commit}
	old, current, draft := assetRef("a"), assetRef("b"), assetRef("d")
	published := Revision{Text: "![I](i.png)", SHA: "p", Assets: map[string]BlobRef{"i.png": old}}
	return Database{
		Sources: map[string]Source{
			"pub":  {ID: "pub", Kind: "wiki", Visibility: "public", ConnectionID: "pub", ImportCommit: commit, Assets: map[string]BlobRef{"i.png": current}},
			"priv": {ID: "priv", Kind: "wiki", Visibility: "private", Assets: map[string]BlobRef{"i.png": assetRef("e")}},
		},
		Connections: map[string]GitHubConnection{"pub": conn},
		Articles: map[string]Article{
			"pub/a.md": {SourceID: "pub", Path: "a.md", Published: &published, Versions: []Revision{
				{Number: 1, Text: "![I](i.png)", Assets: map[string]BlobRef{"i.png": old}},
				{Number: 2, Text: "![I](i.png) draft", Assets: map[string]BlobRef{"i.png": draft}},
			}},
			"pub/gone.md": {SourceID: "pub", Path: "gone.md", Deleted: true, Versions: []Revision{{Number: 1, Assets: map[string]BlobRef{"i.png": assetRef("f")}}}},
			"priv/a.md":   {SourceID: "priv", Path: "a.md", Versions: []Revision{{Number: 1, Assets: map[string]BlobRef{"i.png": assetRef("e")}}}},
		},
	}
}

func TestReadPageCarriesTheDisplayedRevisionImages(t *testing.T) {
	now := time.Now()
	db := publicConnected(now)
	page, err := ReadPage(db, "pub/a.md", true, now)
	if err != nil || page.Assets["i.png"] != assetRef("a") {
		t.Fatal("public page does not use the published images", page.Assets, err)
	}
	page, err = ReadPage(db, "pub/a.md", false, now)
	if err != nil || page.Assets["i.png"] != assetRef("d") {
		t.Fatal("private page does not use the latest revision images", page.Assets, err)
	}
	a := db.Articles["pub/a.md"]
	a.Versions[1].Assets = nil
	db.Articles["pub/a.md"] = a
	if page, _ = ReadPage(db, "pub/a.md", false, now); page.Assets != nil {
		t.Fatal("revision without images borrowed others", page.Assets)
	}
}

func TestRevisionImageVisibility(t *testing.T) {
	now := time.Now()
	db := publicConnected(now)
	for _, tc := range []struct {
		name, source, sha      string
		public, owner, visible bool
	}{
		{"public published image", "pub", assetRef("a").SHA, true, false, true},
		{"public unpublished revision image", "pub", assetRef("d").SHA, true, false, false},
		{"public current source image not in a shown version", "pub", assetRef("b").SHA, true, false, false},
		{"public deleted article image", "pub", assetRef("f").SHA, true, false, false},
		{"public private source image", "priv", assetRef("e").SHA, true, false, false},
		{"owner published image", "pub", assetRef("a").SHA, false, true, true},
		{"owner stored revision image", "pub", assetRef("d").SHA, false, true, true},
		{"owner older stored revision image", "pub", assetRef("a").SHA, false, true, true},
		{"owner deleted article image", "pub", assetRef("f").SHA, false, true, false},
		{"owner private source image", "priv", assetRef("e").SHA, false, true, true},
		{"reader latest revision image", "pub", assetRef("d").SHA, false, false, true},
		{"reader older revision and publication image", "pub", assetRef("a").SHA, false, false, false},
		{"reader deleted article image", "pub", assetRef("f").SHA, false, false, false},
		{"reader private source image", "priv", assetRef("e").SHA, false, false, true},
		{"unknown source", "none", assetRef("e").SHA, false, true, false},
		{"unknown digest", "pub", assetRef("9").SHA, false, true, false},
	} {
		ref, ok := RevisionImage(db, tc.source, "i.png", tc.sha, tc.public, tc.owner, now)
		if ok != tc.visible || (ok && ref.SHA != tc.sha) {
			t.Error(tc.name, ok, ref)
		}
	}
	if _, ok := RevisionImage(db, "pub", "other.png", assetRef("a").SHA, false, true, now); ok {
		t.Error("image of another path served")
	}
	// A local public source shows its latest revision to the public.
	src := db.Sources["priv"]
	src.Visibility = "public"
	db.Sources["priv"] = src
	if _, ok := RevisionImage(db, "priv", "i.png", assetRef("e").SHA, true, false, now); !ok {
		t.Error("public local source image hidden")
	}
}
