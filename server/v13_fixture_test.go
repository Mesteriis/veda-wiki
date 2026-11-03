package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Export only synthetic data for the opt-in offline backup/restore rehearsal.
func TestExportV13RestoreFixture(t *testing.T) {
	dir := os.Getenv("VEDA_V13_FIXTURE_DIR")
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
	if _, err = s.put("from", "a.md", "# Synthetic fixture\n\n![icon](/img/icon.png)\n\n[Related](/related.md)\n\n"+strings.Repeat("тест ", 70000), 0); err != nil {
		t.Fatal(err)
	}
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Synthetic target"})
	f.modify(func() { f.private = true })
	connectFixture(t, s, f, true)
	m, err := s.moveArticle(moveRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "b.md", ConfirmMove: true, IncludeLinked: true})
	if err != nil || m.State != "waiting_destination" || s.db.Version != 13 {
		t.Fatal("fixture move", err, m)
	}
	if err = s.pauseConnection("handbook", true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.createAccessKey("fixture", []string{"from"}, time.Now().Add(30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	db := s.clone()
	key := db.AccessKeys["fixture"]
	hash := sha256.Sum256([]byte(strings.Repeat("e", 64)))
	key.Hash = hex.EncodeToString(hash[:])
	db.AccessKeys[key.ID] = key
	err = s.commit(db)
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"owner.key": strings.Repeat("d", 64), "reader.key": strings.Repeat("f", 64)} {
		if err = os.WriteFile(filepath.Join(dir, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	reopened, err := openStore(dir)
	if err != nil || reopened.db.Version != 13 || len(reopened.jobs()) != 1 || reopened.jobs()[0].Assets["img/icon.png"] != ref || reopened.jobs()[0].Bundle["related.md"] != digest([]byte("# Related fixture")) || !reopened.db.Connections["handbook"].Paused {
		t.Fatal("fixture validation", err)
	}
}

// Resumes only the synthetic state extracted from the offline PBS fixture.
func TestResumeV13RestoreFixture(t *testing.T) {
	dir := os.Getenv("VEDA_V13_RESTORED_DIR")
	if dir == "" {
		t.Skip("restored fixture not provided")
	}
	s, err := openStore(dir)
	if err != nil || s.db.Version != 13 || !s.db.Connections["handbook"].Paused {
		t.Fatal("invalid restored fixture", err)
	}
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Synthetic target"})
	f.modify(func() { f.private = true })
	if err = s.pauseConnection("handbook", false); err != nil {
		t.Fatal(err)
	}
	m := newSyncManager(s, f.client())
	m.Once(context.Background())
	m.Once(context.Background())
	if len(s.jobs()) != 1 || s.jobs()[0].State != "complete" || len(s.moves()) != 1 || s.moves()[0].State != "complete" || !s.db.Articles["from/a.md"].Deleted {
		t.Fatal("restored queue did not complete")
	}
	ref := s.db.Sources["handbook"].Assets["img/icon.png"]
	if ref.SHA == "" || digest([]byte(f.contents()["img/icon.png"])) != ref.SHA || len(f.contents()["b.md"]) <= legacyArticleBytes || f.contents()["related.md"] != "# Related fixture" {
		t.Fatal("restored image or article missing")
	}
}
