package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// These two tests are driven by scripts/bench_migrate_revisions.py and skip
// unless their environment variable names a directory. The data is synthetic.

const (
	benchArticles  = 550
	benchRevisions = 10
	benchImages    = 150
	benchInSource  = 120 // images the source still lists; the rest are stray
	benchUsed      = 100 // images some revision text references
)

// benchPNG returns a noisy PNG of roughly 150 KiB, distinct per index.
func benchPNG(i int) []byte {
	rng := rand.New(rand.NewSource(int64(i) + 1))
	img := image.NewNRGBA(image.Rect(0, 0, 220, 220))
	for y := 0; y < 220; y++ {
		for x := 0; x < 220; x++ {
			img.Set(x, y, color.NRGBA{uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256)), 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		panic(err)
	}
	return b.Bytes()
}

// TestBenchBuildLegacyStore writes a format 20 SQLite store in the legacy
// layout (texts inside the article records) into $VEDA_BENCH_BUILD.
func TestBenchBuildLegacyStore(t *testing.T) {
	dir := os.Getenv("VEDA_BENCH_BUILD")
	if dir == "" {
		t.Skip("VEDA_BENCH_BUILD not set")
	}
	if err := os.MkdirAll(filepath.Join(dir, "blobs"), 0700); err != nil {
		t.Fatal(err)
	}
	refs := make([]domain.BlobRef, benchImages)
	for i := range refs {
		content := benchPNG(i)
		ref, err := domain.ImageRef(content)
		if err != nil {
			t.Fatal(err)
		}
		refs[i] = ref
		if err = os.WriteFile(filepath.Join(dir, "blobs", ref.SHA), content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	assets := map[string]domain.BlobRef{}
	for i := 0; i < benchInSource; i++ {
		assets[fmt.Sprintf("img/%03d.png", i)] = refs[i]
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"version":14,"backend":"sqlite"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, SQLiteFile), nil, 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sqliteDB(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TABLE records (category TEXT NOT NULL, id TEXT NOT NULL, data BLOB NOT NULL, PRIMARY KEY (category,id)) WITHOUT ROWID`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	put := func(category, id string, v any) {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(`INSERT INTO records (category,id,data) VALUES (?,?,?)`, category, id, b); err != nil {
			t.Fatal(err)
		}
	}
	created := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	put("meta", "state", sqliteMeta{Version: 20, Updated: created.Format(time.RFC3339)})
	put("source", "bench", domain.Source{ID: "bench", Title: "Synthetic bench", Kind: "wiki", Visibility: "private", Assets: assets})
	for a := 0; a < benchArticles; a++ {
		article := domain.Article{SourceID: "bench", Path: fmt.Sprintf("area%02d/article%03d.md", a%25, a)}
		for r := 1; r <= benchRevisions; r++ {
			text := fmt.Sprintf("# Synthetic article %d\n\nRevision %d.\n\n", a, r)
			if (a+r)%4 == 0 {
				text += fmt.Sprintf("![figure](../img/%03d.png)\n\n", (a*7+r)%benchUsed)
			}
			for line := 0; line < 40; line++ {
				text += fmt.Sprintf("Synthetic paragraph %d of article %d, revision %d: filler text for sizing.\n", line, a, r)
			}
			article.Versions = append(article.Versions, domain.Revision{Number: r, Text: text, SHA: domain.Digest([]byte(text)), Created: created.Add(time.Duration(a*benchRevisions+r) * time.Minute).Format(time.RFC3339)})
		}
		put("article", "bench/"+article.Path, article)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// TestBenchCollectBlobs opens the converted store in $VEDA_BENCH_COLLECT and
// prints the cost of one blob collection, which holds the store write lock
// for its whole run. The script removes the backup and ages the blobs first.
func TestBenchCollectBlobs(t *testing.T) {
	dir := os.Getenv("VEDA_BENCH_COLLECT")
	if dir == "" {
		t.Skip("VEDA_BENCH_COLLECT not set")
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Open already collected the images the migrated records no longer
	// reference, so add 30 aged stray blobs of image size to sweep.
	old := time.Now().Add(-48 * time.Hour)
	for i := 0; i < 30; i++ {
		p := filepath.Join(dir, "blobs", domain.Digest([]byte(fmt.Sprint("stray", i))))
		if err = os.WriteFile(p, benchPNG(1000+i), 0600); err != nil {
			t.Fatal(err)
		}
		if err = os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	for _, label := range []string{"sweeping_30", "sweeping_none"} {
		start := time.Now()
		removed, err := s.CollectBlobs(time.Now())
		elapsed := time.Since(start)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("collect_%s_ms=%.2f removed=%d\n", label, float64(elapsed.Microseconds())/1000, removed)
	}
}
