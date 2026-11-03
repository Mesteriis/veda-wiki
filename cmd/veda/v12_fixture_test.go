package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Export only synthetic data for the opt-in offline backup/restore rehearsal.
func TestExportV12RestoreFixture(t *testing.T) {
	dir := os.Getenv("VEDA_V12_FIXTURE_DIR")
	if dir == "" {
		t.Skip("fixture export not requested")
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatal("fixture directory must not already exist")
	}
	s, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	source(t, s, "from", "private")
	if _, err = s.put("from", "related.md", "# Related fixture", 0); err != nil {
		t.Fatal(err)
	}
	ref := fixtureAsset(t, s, "from", "img/icon.png")
	if _, err = s.put("from", "a.md", "# Synthetic fixture\n\n![icon](/img/icon.png)\n\n[Related](/related.md)", 0); err != nil {
		t.Fatal(err)
	}
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Synthetic target"})
	f.Modify(func() { f.Private = true })
	connectFixture(t, s, f, true)
	m, err := s.moveArticle(moveRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "b.md", ConfirmMove: true, IncludeLinked: true})
	if err != nil || m.State != "waiting_destination" || s.DB().Version != 12 {
		t.Fatal("fixture move", err, m)
	}
	if err = s.pauseConnection("handbook", true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.auth().CreateAccessKey("fixture", []string{"from"}, time.Now().Add(30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	s.Lock()
	db := s.Clone()
	key := db.AccessKeys["fixture"]
	hash := sha256.Sum256([]byte(strings.Repeat("e", 64)))
	key.Hash = hex.EncodeToString(hash[:])
	db.AccessKeys[key.ID] = key
	err = s.Commit(db)
	s.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"owner.key": strings.Repeat("d", 64), "reader.key": strings.Repeat("f", 64)} {
		if err = os.WriteFile(filepath.Join(dir, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	reopened, err := openStore(dir)
	if err != nil || reopened.DB().Version != 12 || len(reopened.jobs()) != 1 || reopened.jobs()[0].Assets["img/icon.png"] != ref || reopened.jobs()[0].Bundle["related.md"] != digest([]byte("# Related fixture")) || !reopened.DB().Connections["handbook"].Paused {
		t.Fatal("fixture validation", err)
	}
}

// Resumes only the synthetic state extracted from the offline PBS fixture.
func TestResumeV12RestoreFixture(t *testing.T) {
	dir := os.Getenv("VEDA_V12_RESTORED_DIR")
	if dir == "" {
		t.Skip("restored fixture not provided")
	}
	s, err := openStore(dir)
	if err != nil || s.DB().Version != 12 || !s.DB().Connections["handbook"].Paused {
		t.Fatal("invalid restored fixture", err)
	}
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Synthetic target"})
	f.Modify(func() { f.Private = true })
	if err = s.pauseConnection("handbook", false); err != nil {
		t.Fatal(err)
	}
	m := newSyncManager(s, f.Client())
	m.Once(context.Background())
	m.Once(context.Background())
	if len(s.jobs()) != 1 || s.jobs()[0].State != "complete" || len(s.moves()) != 1 || s.moves()[0].State != "complete" || !s.DB().Articles["from/a.md"].Deleted {
		t.Fatal("restored queue did not complete")
	}
	ref := s.DB().Sources["handbook"].Assets["img/icon.png"]
	if ref.SHA == "" || digest([]byte(f.Contents()["img/icon.png"])) != ref.SHA || f.Contents()["b.md"] == "" || f.Contents()["related.md"] != "# Related fixture" {
		t.Fatal("restored image or article missing")
	}
}
