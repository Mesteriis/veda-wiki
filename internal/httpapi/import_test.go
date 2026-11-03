package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/importer"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
	"github.com/Mesteriis/veda-wiki/internal/render"
)

const metadataFixture = "schema_version: 1\nkind: wiki\ntitle: Handbook\ndescription: Team documentation\n"

func testBatch() importer.Batch {
	empty := ""
	return importer.Batch{SourceID: "handbook", Kind: "wiki", Metadata: &domain.WikiMetadata{SchemaVersion: 1, Kind: "wiki", Title: "Handbook"}, Files: map[string]string{"README.md": "# Initial"}, Commit: strings.Repeat("a", 40), Complete: true, Create: true, Visibility: "private", ExpectedImport: &empty}
}
func applyAgain(s *Store, b importer.Batch) importer.Batch {
	b.Create = false
	g := s.DB().Sources[b.SourceID].ImportGeneration
	b.ExpectedImport = &g
	return b
}
func smallPNG(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	if e := png.Encode(&out, image.NewRGBA(image.Rect(0, 0, 2, 2))); e != nil {
		t.Fatal(e)
	}
	return out.Bytes()
}

func TestUnchangedImportRepairsMissingOrCorruptAssets(t *testing.T) {
	for _, damage := range []string{"missing", "corrupt", "symlink"} {
		t.Run(damage, func(t *testing.T) {
			s := testStore(t)
			b := testBatch()
			b.Assets = map[string][]byte{"images/test.png": smallPNG(t)}
			first, err := s.importBatch(b)
			if err != nil {
				t.Fatal(err)
			}
			b = applyAgain(s, b)
			ref := s.DB().Sources[b.SourceID].Assets["images/test.png"]
			filename := filepath.Join(s.Dir(), "blobs", ref.SHA)
			if err = os.Remove(filename); err != nil {
				t.Fatal(err)
			}
			switch damage {
			case "corrupt":
				err = os.WriteFile(filename, []byte("corrupt"), 0600)
			case "symlink":
				target := filepath.Join(t.TempDir(), "target")
				if err = os.WriteFile(target, []byte("unchanged"), 0600); err != nil {
					t.Fatal(err)
				}
				err = os.Symlink(target, filename)
				defer func() {
					content, readErr := os.ReadFile(target)
					if readErr != nil || string(content) != "unchanged" {
						t.Error("symlink target modified", readErr)
					}
				}()
			}
			if err != nil {
				t.Fatal(err)
			}
			result, err := s.importBatch(b)
			if err != nil || result.Generation != first.Generation || result.Unchanged != 1 || result.Created != 0 || result.Updated != 0 || result.Conflicts != 0 {
				t.Fatal("unchanged import failed", result, err)
			}
			if err = s.verifyAsset(ref); err != nil {
				t.Fatal("asset not repaired", err)
			}
			media := request(Wiki{Store: s, Token: "test-owner"}, "GET", "/media/handbook/images/test.png", "", true)
			if media.Code != 200 || !bytes.Equal(media.Body.Bytes(), b.Assets["images/test.png"]) {
				t.Fatal("repaired asset not served", media.Code)
			}
			article, err := s.article(b.SourceID, "README.md", false)
			if err != nil || len(article.Versions) != 1 {
				t.Fatal("article history changed", err)
			}
		})
	}
}

func TestPrivateImportPreservesImagesNeededByLocalEdits(t *testing.T) {
	s := testStore(t)
	b := testBatch()
	b.Visibility = "private"
	b.Files = map[string]string{"README.md": "# Original\n\n![image](images/keep.png)", "clean.md": "# Clean\n\n![other](images/remove.png)"}
	b.Assets = map[string][]byte{"images/keep.png": smallPNG(t), "images/remove.png": smallPNG(t)}
	if _, err := s.importBatch(b); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("handbook", "README.md", "# Local edit\n\n![image](images/keep.png)", 1); err != nil {
		t.Fatal(err)
	}
	b = applyAgain(s, b)
	b.Files, b.Assets = map[string]string{}, map[string][]byte{}
	if _, err := s.importBatch(b); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.DB().Sources["handbook"].Assets["images/keep.png"]; !ok {
		t.Fatal("local edit lost its image")
	}
	if _, ok := s.DB().Sources["handbook"].Assets["images/remove.png"]; ok {
		t.Fatal("unreferenced upstream image retained")
	}
	if len(s.conflicts()) != 1 || !s.DB().Articles["handbook/clean.md"].Deleted {
		t.Fatal("deletion handling changed")
	}
	r := request(Wiki{Store: s, Token: "test-owner"}, "GET", "/media/handbook/images/keep.png", "", true)
	if r.Code != 200 {
		t.Fatal("retained image unavailable", r.Code)
	}
}

func TestLocalImportRejectsMissingRetainedImage(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(fmt.Sprintf("changed=%t", changed), func(t *testing.T) {
			s := testStore(t)
			b := testBatch()
			b.Visibility = "private"
			image := smallPNG(t)
			b.Assets = map[string][]byte{"images/retained.png": image}
			if _, err := s.importBatch(b); err != nil {
				t.Fatal(err)
			}
			b = applyAgain(s, b)
			b.Assets = map[string][]byte{}
			b.Complete, b.Withheld = false, 1
			if _, err := s.importBatch(b); err != nil {
				t.Fatal(err)
			}
			b = applyAgain(s, b)
			if changed {
				b.Files["README.md"] = "# Changed"
			}
			before := observedAt(s, b.SourceID)
			generation := s.DB().Sources[b.SourceID].ImportGeneration
			ref := s.DB().Sources[b.SourceID].Assets["images/retained.png"]
			filename := filepath.Join(s.Dir(), "blobs", ref.SHA)
			if err := os.Remove(filename); err != nil {
				t.Fatal(err)
			}
			if _, err := s.importBatch(b); !errors.Is(err, importer.ErrAssetUnavailable) {
				t.Fatal("missing retained image was accepted", err)
			}
			payload, err := json.Marshal(b)
			if err != nil {
				t.Fatal(err)
			}
			response := request(Wiki{Store: s, Token: "test-owner"}, "POST", "/api/v1/import", string(payload), true)
			var body map[string]string
			if response.Code != 503 || json.Unmarshal(response.Body.Bytes(), &body) != nil || body["error"] != "asset_unavailable" {
				t.Fatal("import did not report the asset error", response.Code, response.Body.String())
			}
			if !observedAt(s, b.SourceID).Equal(before) || s.DB().Sources[b.SourceID].ImportGeneration != generation {
				t.Fatal("failed import advanced the source")
			}
			if err := os.WriteFile(filename, image, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := s.importBatch(b); err != nil {
				t.Fatal("import did not recover", err)
			}
		})
	}
}

func TestPrivateImagesAreNotRetainedOnGitHubPublication(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"docs/guide.md": "# Original\n\n![image](image.png)", "docs/image.png": string(smallPNG(t))})
	f.Modify(func() { f.Private = true })
	m := connectFixture(t, s, f, false)
	if _, err := s.put("handbook", "docs/guide.md", "# Local edit\n\n![image](image.png)", 1); err != nil {
		t.Fatal(err)
	}
	f.Modify(func() { f.Private = false })
	f.Advance(map[string]string{"src/main.go": "package main"})
	m.Once(context.Background())
	if _, ok := s.DB().Sources["handbook"].Assets["docs/image.png"]; ok {
		t.Fatal("private image survived publication")
	}
	if got := request(Wiki{Store: s, Public: true}, "GET", "/media/handbook/docs/image.png", "", false); got.Code != 404 {
		t.Fatal("private image published", got.Code)
	}
}
func TestImportMediaPrivacyIntegrityAndMarkdownLinks(t *testing.T) {
	s := testStore(t)
	b := testBatch()
	b.Metadata.Image = "assets/cover.png"
	b.Assets = map[string][]byte{"assets/cover.png": smallPNG(t)}
	b.Files["README.md"] = "# Page\n\n![Cover](assets/cover.png)"
	if _, e := s.importBatch(b); e != nil {
		t.Fatal(e)
	}
	w := Wiki{Store: s, Token: "test-owner"}
	if r := request(w, "GET", "/media/handbook/assets/cover.png", "", false); r.Code != 404 {
		t.Fatal("private image disclosed")
	}
	if r := request(w, "GET", "/media/handbook/assets/cover.png", "", true); r.Code != 200 || r.Header().Get("Content-Type") != "image/png" {
		t.Fatal(r.Code)
	}
	p, _ := s.page("handbook/README.md", false)
	body, _, e := render.Page(p)
	if e != nil || !strings.Contains(string(body), "/media/handbook/assets%2Fcover.png") {
		t.Fatal(body, e)
	}
	ref := s.DB().Sources["handbook"].Assets["assets/cover.png"]
	os.WriteFile(filepath.Join(s.Dir(), "blobs", ref.SHA), []byte("corrupt"), 0600)
	if r := request(w, "GET", "/media/handbook/assets/cover.png", "", true); r.Code != 503 {
		t.Fatal("corrupt asset served", r.Code)
	}
	w.Public = true
	if r := request(w, "GET", "/media/handbook/assets/cover.png", "", true); r.Code != 404 {
		t.Fatal("public listener image bypass")
	}
}
func TestImportAndConflictRESTBoundaries(t *testing.T) {
	s := testStore(t)
	w := Wiki{Store: s, Token: "test-owner"}
	b := testBatch()
	payload, _ := json.Marshal(b)
	if r := request(w, "POST", "/api/v1/import", string(payload), false); r.Code != 403 {
		t.Fatal(r.Code)
	}
	if r := request(w, "POST", "/api/v1/import", string(payload), true); r.Code != 200 {
		t.Fatal(r.Code, r.Body)
	}
	for _, route := range []string{"/api/v1/conflicts", "/api/v1/import"} {
		if r := request(w, "GET", route, "", false); r.Code != 404 {
			t.Fatal(route, r.Code)
		}
	}
	s.put("handbook", "README.md", "# Local", 1)
	b = applyAgain(s, b)
	b.Files = map[string]string{"README.md": "# Remote private canary"}
	payload, _ = json.Marshal(b)
	request(w, "POST", "/api/v1/import", string(payload), true)
	if r := request(w, "GET", "/api/v1/conflicts?source_id=handbook&path=README.md&index=0", "", true); r.Code != 200 || !strings.Contains(r.Body.String(), "Remote private canary") {
		t.Fatal(r.Code)
	}
	if r := request(w, "POST", "/api/v1/conflicts/resolve", `{"source_id":"handbook","path":"README.md","index":0,"expected_revision":2,"resolution":"upstream"}`, true); r.Code != 200 {
		t.Fatal(r.Code, r.Body)
	}
}
