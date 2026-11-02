package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSQLiteMigrationAndRestart(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	if _, err := s.put("local", "guide.md", "# First", 0); err != nil {
		t.Fatal(err)
	}
	if err := migrateSQLite(s.dir); err != nil {
		t.Fatal(err)
	}
	marker, err := os.ReadFile(filepath.Join(s.dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m sqliteMarker
	if json.Unmarshal(marker, &m) != nil || m.Version != 14 || m.Backend != "sqlite" {
		t.Fatal("invalid marker")
	}
	previous, err := os.ReadFile(filepath.Join(s.dir, "state.before-sqlite.json"))
	if err != nil || !strings.Contains(string(previous), "# First") {
		t.Fatal("missing JSON backup", err)
	}
	reopened, err := openStore(s.dir)
	if err != nil || !reopened.sqlite {
		t.Fatal("cannot open SQLite", err)
	}
	db, err := sqliteDB(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	var journal string
	var syncMode, tempMode int
	if db.QueryRow("PRAGMA journal_mode").Scan(&journal) != nil || db.QueryRow("PRAGMA synchronous").Scan(&syncMode) != nil || db.QueryRow("PRAGMA temp_store").Scan(&tempMode) != nil || journal != "delete" || syncMode != 3 || tempMode != 2 {
		t.Fatal("SQLite durability or temporary storage settings are incorrect")
	}
	db.Close()
	if _, err = reopened.put("local", "guide.md", "# Second", 1); err != nil {
		t.Fatal(err)
	}
	again, err := openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	page, err := again.page("local/guide.md", false)
	if err != nil || page.Text != "# Second" {
		t.Fatal("revision not durable", err)
	}
	original, err := os.ReadFile(filepath.Join(s.dir, "state.before-sqlite.json"))
	if err != nil || string(original) != string(previous) {
		t.Fatal("backup changed", err)
	}
	if err = migrateSQLite(s.dir); err == nil {
		t.Fatal("repeat migration accepted")
	}
}

func TestSQLiteMigrationRestoresPendingSync(t *testing.T) {
	s := testStore(t)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Original"})
	connectFixture(t, s, f, true)
	if _, err := s.put("handbook", "README.md", "# Local update", 1); err != nil {
		t.Fatal(err)
	}
	if err := migrateSQLite(s.dir); err != nil {
		t.Fatal(err)
	}
	s, err := openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	m := newSyncManager(s, f.client())
	m.Once(context.Background())
	m.Once(context.Background())
	if f.writeCount() != 1 || len(s.jobs()) != 1 || s.jobs()[0].State != "complete" {
		t.Fatal("pending sync not resumed", s.jobs())
	}
	reopened, err := openStore(s.dir)
	if err != nil || reopened.jobs()[0].State != "complete" {
		t.Fatal("completed sync not durable", err)
	}
}

func TestSQLiteOfflineBackupRestores(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	if _, err := s.put("local", "guide.md", "# Restored", 0); err != nil {
		t.Fatal(err)
	}
	if err := migrateSQLite(s.dir); err != nil {
		t.Fatal(err)
	}
	restore := filepath.Join(t.TempDir(), "restore")
	if err := os.Mkdir(restore, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"state.json", sqliteFile} {
		data, err := os.ReadFile(filepath.Join(s.dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(restore, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	reopened, err := openStore(restore)
	if err != nil {
		t.Fatal(err)
	}
	page, err := reopened.page("local/guide.md", false)
	if err != nil || page.Text != "# Restored" {
		t.Fatal("restored store lost article", err)
	}
}

func TestSQLiteMigrationRequiresExclusiveLock(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	lock, err := os.OpenFile(filepath.Join(s.dir, "writer.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if err = migrateSQLite(s.dir); err == nil {
		t.Fatal("migrated while server lock held")
	}
	if _, err = os.Stat(filepath.Join(s.dir, sqliteFile)); !os.IsNotExist(err) {
		t.Fatal("locked migration created SQLite")
	}
}

func TestOldBinaryRejectsSQLiteMarker(t *testing.T) {
	old := os.Getenv("VEDA_OLD_SQLITE_BINARY")
	if old == "" {
		t.Skip("old binary not provided")
	}
	s := testStore(t)
	source(t, s, "local", "private")
	if err := migrateSQLite(s.dir); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, old, "--data", s.dir, "--listen", "127.0.0.1:0", "--public-listen", "127.0.0.1:0").CombinedOutput()
	if err == nil || ctx.Err() != nil || !strings.Contains(string(out), "unsupported store") {
		t.Fatal("old binary did not reject SQLite marker")
	}
	if _, err = openStore(s.dir); err != nil {
		t.Fatal("old binary modified the migrated store", err)
	}
}

func TestSQLiteCommitFailureFailsClosed(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	if err := migrateSQLite(s.dir); err != nil {
		t.Fatal(err)
	}
	s, err := openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sqliteDB(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("DROP TABLE records"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err = s.put("local", "guide.md", "# Update", 0); err == nil {
		t.Fatal("failed commit accepted")
	}
	out := httptest.NewRecorder()
	Wiki{Store: s}.ServeHTTP(out, httptest.NewRequest("GET", "/health", nil))
	if out.Code != 503 {
		t.Fatal("invalid store remained available", out.Code)
	}
	if _, err = s.put("local", "another.md", "# Update", 0); err == nil {
		t.Fatal("failed store accepted another write")
	}
}

func TestSQLiteStoreExceedsJSONCapacity(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	if err := migrateSQLite(s.dir); err != nil {
		t.Fatal(err)
	}
	s, err := openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	state := s.clone()
	text := strings.Repeat("x", maxArticleBytes)
	sha := digest([]byte(text))
	state.Version = 13
	for i := 0; i < 65; i++ {
		p := fmt.Sprintf("%02d.md", i)
		state.Articles["local/"+p] = Article{SourceID: "local", Path: p, Versions: []Revision{{Number: 1, Text: text, SHA: sha}}}
	}
	if err = s.commit(state); err != nil {
		t.Fatal("SQLite rejected large store", err)
	}
	reopened, err := openStore(s.dir)
	if err != nil || len(reopened.db.Articles) != 65 {
		t.Fatal("large SQLite store did not reopen", err)
	}
}
