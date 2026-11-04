package commands

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// copyStoreFixture copies a store fixture into a private data directory.
func copyStoreFixture(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "data")
	root := filepath.Join("..", "..", "store", "testdata", name)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dir, rel), 0700)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, rel), data, 0600)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestMigrateRevisionsPrintsOnlyCounts(t *testing.T) {
	dir := copyStoreFixture(t, "v20-history")
	var out bytes.Buffer
	if err := MigrateRevisions([]string{"--data", dir}, &out); err != nil {
		t.Fatal(err)
	}
	want := "articles: 10\nkept revisions: 27\nremoved revisions: 9\nblob cleanup paused until state.before-revisions.* is removed\n"
	if out.String() != want {
		t.Fatalf("output %q", out.String())
	}
	for _, secret := range []string{dir, "notes", "guide", "Synthetic", "img/", ".png", "sha"} {
		if strings.Contains(out.String(), secret) {
			t.Fatal("output names", secret)
		}
	}
	out.Reset()
	if err := MigrateRevisions([]string{"--verify", "--data", dir}, &out); err != nil || out.String() != "articles: 10\nkept revisions: 27\nremoved revisions: 9\nverify: OK\n" {
		t.Fatalf("verify %q %v", out.String(), err)
	}
	out.Reset()
	err := MigrateRevisions([]string{"--data", dir}, &out)
	if err == nil || out.Len() != 0 || strings.Contains(err.Error(), dir) {
		t.Fatal("second run", err, out.String())
	}
	if err = MigrateRevisions([]string{"--data", dir, "extra"}, &out); err == nil {
		t.Fatal("extra argument accepted")
	}
	out.Reset()
	unconverted := copyStoreFixture(t, "v20-history")
	err = MigrateRevisions([]string{"--verify", "--data", unconverted}, &out)
	if err == nil || out.String() != "verify: FAIL\n" || strings.Contains(err.Error(), unconverted) {
		t.Fatalf("verify without a migration %q %v", out.String(), err)
	}
}
