package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func update(t *testing.T, s *Store, fn func(db *domain.Database)) {
	t.Helper()
	if err := s.Update(func(tx *Tx) error { fn(tx.DB()); return nil }); err != nil {
		t.Fatal(err)
	}
}

func addSource(t *testing.T, s *Store, id string) {
	t.Helper()
	update(t, s, func(db *domain.Database) {
		db.Sources[id] = domain.Source{ID: id, Title: id, Kind: "wiki", Visibility: "private"}
	})
}

// appendRevision returns db with a new latest revision of sourceID/p.
func appendRevision(db *domain.Database, sourceID, p, text string) {
	key := sourceID + "/" + p
	a := db.Articles[key]
	a.SourceID, a.Path = sourceID, p
	a.Versions = append(append([]domain.Revision(nil), a.Versions...), domain.Revision{Number: len(a.Versions) + 1, Text: text, SHA: domain.Digest([]byte(text))})
	db.Articles[key] = a
}

func latest(t *testing.T, s *Store, key string) string {
	t.Helper()
	var text string
	if err := s.View(func(db *domain.Database) error {
		a, ok := db.Articles[key]
		if !ok {
			return domain.ErrNotFound
		}
		text = a.Versions[len(a.Versions)-1].Text
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return text
}

func TestSQLiteMigrationAndRestart(t *testing.T) {
	s := testStore(t)
	addSource(t, s, "local")
	update(t, s, func(db *domain.Database) { appendRevision(db, "local", "guide.md", "# First") })
	if err := MigrateSQLite(s.Dir()); err != nil {
		t.Fatal(err)
	}
	marker, err := os.ReadFile(filepath.Join(s.Dir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m sqliteMarker
	if json.Unmarshal(marker, &m) != nil || m.Version != 14 || m.Backend != "sqlite" {
		t.Fatal("invalid marker")
	}
	previous, err := os.ReadFile(filepath.Join(s.Dir(), "state.before-sqlite.json"))
	if err != nil || !strings.Contains(string(previous), "# First") {
		t.Fatal("missing JSON backup", err)
	}
	reopened, err := Open(s.Dir())
	if err != nil || !reopened.IsSQLite() {
		t.Fatal("cannot open SQLite", err)
	}
	db, err := sqliteDB(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	var journal string
	var syncMode, tempMode int
	if db.QueryRow("PRAGMA journal_mode").Scan(&journal) != nil || db.QueryRow("PRAGMA synchronous").Scan(&syncMode) != nil || db.QueryRow("PRAGMA temp_store").Scan(&tempMode) != nil || journal != "delete" || syncMode != 3 || tempMode != 2 {
		t.Fatal("SQLite durability or temporary storage settings are incorrect")
	}
	db.Close()
	if err = reopened.Update(func(tx *Tx) error {
		appendRevision(tx.DB(), "local", "guide.md", "# Second")
		tx.OnlyArticle("local/guide.md")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	again, err := Open(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if text := latest(t, again, "local/guide.md"); text != "# Second" {
		t.Fatal("revision not durable", text)
	}
	original, err := os.ReadFile(filepath.Join(s.Dir(), "state.before-sqlite.json"))
	if err != nil || string(original) != string(previous) {
		t.Fatal("backup changed", err)
	}
	if err = MigrateSQLite(s.Dir()); err == nil {
		t.Fatal("repeat migration accepted")
	}
}

func TestSQLiteOfflineBackupRestores(t *testing.T) {
	s := testStore(t)
	addSource(t, s, "local")
	update(t, s, func(db *domain.Database) { appendRevision(db, "local", "guide.md", "# Restored") })
	if err := MigrateSQLite(s.Dir()); err != nil {
		t.Fatal(err)
	}
	restore := filepath.Join(t.TempDir(), "restore")
	if err := os.Mkdir(restore, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"state.json", SQLiteFile} {
		data, err := os.ReadFile(filepath.Join(s.Dir(), name))
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(restore, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	reopened, err := Open(restore)
	if err != nil {
		t.Fatal(err)
	}
	if text := latest(t, reopened, "local/guide.md"); text != "# Restored" {
		t.Fatal("restored store lost article", text)
	}
}

func TestSQLiteMigrationRequiresExclusiveLock(t *testing.T) {
	s := testStore(t)
	addSource(t, s, "local")
	lock, err := os.OpenFile(filepath.Join(s.Dir(), "writer.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if err = MigrateSQLite(s.Dir()); err == nil {
		t.Fatal("migrated while server lock held")
	}
	if _, err = os.Stat(filepath.Join(s.Dir(), SQLiteFile)); !os.IsNotExist(err) {
		t.Fatal("locked migration created SQLite")
	}
}

func TestOldBinaryRejectsSQLiteMarker(t *testing.T) {
	old := os.Getenv("VEDA_OLD_SQLITE_BINARY")
	if old == "" {
		t.Skip("old binary not provided")
	}
	s := testStore(t)
	addSource(t, s, "local")
	if err := MigrateSQLite(s.Dir()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, old, "--data", s.Dir(), "--listen", "127.0.0.1:0", "--public-listen", "127.0.0.1:0").CombinedOutput()
	if err == nil || ctx.Err() != nil || !strings.Contains(string(out), "unsupported store") {
		t.Fatal("old binary did not reject SQLite marker")
	}
	if _, err = Open(s.Dir()); err != nil {
		t.Fatal("old binary modified the migrated store", err)
	}
}

func TestSQLiteStoreExceedsJSONCapacity(t *testing.T) {
	s := testStore(t)
	addSource(t, s, "local")
	if err := MigrateSQLite(s.Dir()); err != nil {
		t.Fatal(err)
	}
	s, err := Open(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Repeat("x", domain.MaxArticleBytes)
	sha := domain.Digest([]byte(text))
	if err = s.Update(func(tx *Tx) error {
		// The store keeps its format: a v21 store has revision rows, which
		// an older format would not read.
		state := tx.DB()
		for i := 0; i < 65; i++ {
			p := fmt.Sprintf("%02d.md", i)
			state.Articles["local/"+p] = domain.Article{SourceID: "local", Path: p, Versions: []domain.Revision{{Number: 1, Text: text, SHA: sha}}}
		}
		return nil
	}); err != nil {
		t.Fatal("SQLite rejected large store", err)
	}
	reopened, err := Open(s.Dir())
	if err != nil {
		t.Fatal("large SQLite store did not reopen", err)
	}
	if n := len(reopened.db.Articles); n != 65 {
		t.Fatal("large SQLite store lost articles", n)
	}
}
