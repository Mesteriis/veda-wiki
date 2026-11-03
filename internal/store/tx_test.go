package store_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store"
	"github.com/Mesteriis/veda-wiki/internal/store/storetest"
)

// The fixture in testdata/fc35bea was written by the deployed commit fc35bea
// from synthetic data only:
//
//	git worktree add $TMP/veda-fc35bea fc35bea && make -C $TMP/veda-fc35bea build
//	veda --data $DIR --listen 127.0.0.1:18731 --public-listen 127.0.0.1:18732
//	POST /api/v1/sources  {"id":"fixture","title":"Synthetic fixture","kind":"wiki","visibility":"private"}
//	POST /api/v1/articles {"source_id":"fixture","path":"guide.md","text":"# Synthetic guide\n\nFirst revision.","expected_revision":0}
//	PUT  /api/v1/articles {... "text":"# Synthetic guide\n\nSecond revision.","expected_revision":1}
//	POST /api/v1/teams    {"id":"fixture-team","sources":["fixture"]}
//	POST /api/v1/users    {"id":"fixture-user","teams":["fixture-team"],"password":"synthetic-password-0001"}
//	veda access-key create --id fixture-reader --sources fixture --expires-at 2027-10-01T00:00:00Z \
//	    --out $TMP/fixture-reader.key --url http://127.0.0.1:18731 --key-file $DIR/owner.key
//	(stop the server) veda migrate-sqlite --data $DIR
//
// Only state.json (the SQLite marker) and state.sqlite were copied. The
// createsuperuser command needs a Linux root peer on the reader socket, so the
// user was created through the owner REST API instead.
func copyFixture(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"state.json", store.SQLiteFile} {
		data, err := os.ReadFile(filepath.Join("testdata", "fc35bea", name))
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestOpenFc35beaSQLite(t *testing.T) {
	st, err := store.Open(copyFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if !st.IsSQLite() {
		t.Fatal("fixture did not open as SQLite")
	}
	db := storetest.Read(t, st)
	a := db.Articles["fixture/guide.md"]
	if len(a.Versions) != 2 || a.Versions[0].Text != "# Synthetic guide\n\nFirst revision." || a.Versions[1].Text != "# Synthetic guide\n\nSecond revision." {
		t.Fatal("article revisions missing", a.Versions)
	}
	if key, ok := db.AccessKeys["fixture-reader"]; !ok || len(key.Sources) != 1 || key.Sources[0] != "fixture" {
		t.Fatal("access key missing", key)
	}
	if user, ok := db.Users["fixture-user"]; !ok || len(user.Teams) != 1 || user.Teams[0] != "fixture-team" {
		t.Fatal("user missing", user)
	}
	if _, ok := db.Teams["fixture-team"]; !ok {
		t.Fatal("team missing")
	}
}

func TestUpdateErrorLeavesMemoryAndDiskUnchanged(t *testing.T) {
	for _, backend := range []string{"json", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			st := storetest.New(t)
			storetest.Source(t, st, "local", "private")
			storetest.Mutate(t, st, func(db *domain.Database) {
				db.Articles["local/guide.md"] = revision("local", "guide.md", "# Original")
			})
			if backend == "sqlite" {
				if err := store.MigrateSQLite(st.Dir()); err != nil {
					t.Fatal(err)
				}
				var err error
				if st, err = store.Open(st.Dir()); err != nil || !st.IsSQLite() {
					t.Fatal("SQLite reopen failed", err)
				}
			}
			rejected := errors.New("rejected")
			err := st.Update(func(tx *store.Tx) error {
				tx.DB().Articles["local/guide.md"] = revision("local", "guide.md", "# Changed")
				return rejected
			})
			if err != rejected {
				t.Fatal("Update did not return the callback error", err)
			}
			if text := storetest.Read(t, st).Articles["local/guide.md"].Versions[0].Text; text != "# Original" {
				t.Fatal("failed update changed memory", text)
			}
			reopened, err := store.Open(st.Dir())
			if err != nil {
				t.Fatal(err)
			}
			if text := storetest.Read(t, reopened).Articles["local/guide.md"].Versions[0].Text; text != "# Original" {
				t.Fatal("failed update changed disk", text)
			}
		})
	}
}

// Sync status changes run twice per connection and cycle. On SQLite they must
// persist only connection records, not reserialize every article's history.
func TestOnlyConnectionsWritesNoArticleRecords(t *testing.T) {
	st := storetest.New(t)
	storetest.Source(t, st, "local", "private")
	storetest.Mutate(t, st, func(db *domain.Database) {
		db.Articles["local/guide.md"] = revision("local", "guide.md", "# Original")
		db.Connections["handbook"] = domain.GitHubConnection{ID: "handbook", Repository: "org/handbook", RepositoryID: 1, Branch: "main", State: "idle", ConfigRevision: 1}
	})
	if err := store.MigrateSQLite(st.Dir()); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	// An article change inside the same transaction reveals whether article
	// rows are rewritten when only connections are declared.
	if err = st.Update(func(tx *store.Tx) error {
		db := tx.DB()
		db.Articles["local/guide.md"] = revision("local", "guide.md", "# Not persisted")
		c := db.Connections["handbook"]
		c.State = "syncing"
		db.Connections["handbook"] = c
		tx.OnlyConnections()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	db := storetest.Read(t, reopened)
	if db.Connections["handbook"].State != "syncing" {
		t.Fatal("connection status was not persisted")
	}
	if text := db.Articles["local/guide.md"].Versions[0].Text; text != "# Original" {
		t.Fatal("connection update rewrote article records", text)
	}
}

func TestScopedStoreRejectsUpdate(t *testing.T) {
	st := storetest.New(t)
	storetest.Source(t, st, "a", "private")
	storetest.Source(t, st, "b", "private")
	storetest.Mutate(t, st, func(db *domain.Database) {
		db.Articles["a/guide.md"] = revision("a", "guide.md", "# A")
		db.Articles["b/guide.md"] = revision("b", "guide.md", "# B")
	})
	scoped, err := st.Scoped([]string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	db := storetest.Read(t, scoped)
	if _, ok := db.Sources["a"]; !ok || len(db.Sources) != 1 {
		t.Fatal("scoped store sources", db.Sources)
	}
	if _, ok := db.Articles["b/guide.md"]; ok || len(db.Articles) != 1 {
		t.Fatal("scoped store exposed another source's article")
	}
	if err = scoped.Update(func(tx *store.Tx) error {
		tx.DB().Articles["a/new.md"] = revision("a", "new.md", "# New")
		return nil
	}); err == nil {
		t.Fatal("scoped store accepted an update")
	}
	if _, ok := storetest.Read(t, scoped).Articles["a/new.md"]; ok {
		t.Fatal("rejected update changed the scoped snapshot")
	}
	reopened, err := store.Open(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := storetest.Read(t, reopened).Articles["a/new.md"]; ok {
		t.Fatal("scoped update reached the data directory")
	}
}

func revision(sourceID, p, text string) domain.Article {
	return domain.Article{SourceID: sourceID, Path: p, Versions: []domain.Revision{{Number: 1, Text: text, SHA: domain.Digest([]byte(text))}}}
}

func TestSQLiteUpdateCommitFailureMatchesDisk(t *testing.T) {
	st := storetest.New(t)
	storetest.Source(t, st, "local", "private")
	storetest.Mutate(t, st, func(db *domain.Database) {
		db.Articles["local/guide.md"] = revision("local", "guide.md", "# Original")
	})
	if err := store.MigrateSQLite(st.Dir()); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	db := storetest.SQLite(t, st.Dir())
	if _, err = db.Exec(`CREATE TRIGGER reject_article BEFORE UPDATE ON records WHEN NEW.category='article' BEGIN SELECT RAISE(ABORT, 'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if err = st.Update(func(tx *store.Tx) error {
		tx.DB().Articles["local/guide.md"] = revision("local", "guide.md", "# Changed")
		tx.OnlyArticle("local/guide.md")
		return nil
	}); err == nil {
		t.Fatal("failed commit accepted")
	}
	if st.Invalid() {
		t.Fatal("recoverable commit failure marked the store invalid")
	}
	reopened, err := store.Open(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	live, disk := storetest.Read(t, st), storetest.Read(t, reopened)
	if live.Articles["local/guide.md"].Versions[0].Text != "# Original" || disk.Articles["local/guide.md"].Versions[0].Text != "# Original" {
		t.Fatal("failed commit changed the article")
	}
	liveJSON, _ := json.Marshal(live)
	diskJSON, _ := json.Marshal(disk)
	if string(liveJSON) != string(diskJSON) {
		t.Fatal("memory differs from the directory after a failed commit")
	}
}

func TestSQLiteUpdateUnrecoverableFailureFailsClosed(t *testing.T) {
	st := storetest.New(t)
	storetest.Source(t, st, "local", "private")
	if err := store.MigrateSQLite(st.Dir()); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	db := storetest.SQLite(t, st.Dir())
	if _, err = db.Exec("DROP TABLE records"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	change := func(tx *store.Tx) error {
		tx.DB().Articles["local/guide.md"] = revision("local", "guide.md", "# Changed")
		return nil
	}
	if err = st.Update(change); err == nil {
		t.Fatal("failed commit accepted")
	}
	if !st.Invalid() {
		t.Fatal("unrecoverable commit failure left the store available")
	}
	if _, ok := storetest.Read(t, st).Articles["local/guide.md"]; ok {
		t.Fatal("failed commit changed memory")
	}
	if err = st.Update(change); err == nil {
		t.Fatal("invalid store accepted another update")
	}
}

func TestAfterCommitRunsOnlyAfterACommit(t *testing.T) {
	st := storetest.New(t)
	storetest.Source(t, st, "local", "private")
	runs := 0
	refused := errors.New("refused")
	if err := st.Update(func(tx *store.Tx) error {
		tx.AfterCommit(func() { runs++ })
		return refused
	}); !errors.Is(err, refused) || runs != 0 {
		t.Fatal("hook ran for a refused transaction", err, runs)
	}
	if err := st.Update(func(tx *store.Tx) error {
		if tx.StoreInvalid() {
			t.Error("healthy store reported invalid")
		}
		tx.DB().Articles["local/guide.md"] = revision("local", "guide.md", "# Committed")
		tx.AfterCommit(func() { runs++ })
		return nil
	}); err != nil || runs != 1 {
		t.Fatal("hook did not run once after the commit", err, runs)
	}
	if err := store.MigrateSQLite(st.Dir()); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	db := storetest.SQLite(t, st.Dir())
	if _, err = db.Exec("DROP TABLE records"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if err = st.Update(func(tx *store.Tx) error {
		tx.DB().Articles["local/guide.md"] = revision("local", "guide.md", "# Failed")
		tx.AfterCommit(func() { runs++ })
		return nil
	}); err == nil || runs != 1 {
		t.Fatal("hook ran after a failed commit", err, runs)
	}
	invalid := false
	_ = st.Update(func(tx *store.Tx) error {
		invalid = tx.StoreInvalid()
		return refused
	})
	if !invalid {
		t.Fatal("transaction did not report the failed-closed store")
	}
}

func TestExclusiveKeepsEarlierCommitsAndDiscardsAFailedStep(t *testing.T) {
	st := storetest.New(t)
	storetest.Source(t, st, "local", "private")
	refused := errors.New("refused")
	err := st.Exclusive(func(ex *store.Exclusive) error {
		if ex.Invalid() {
			t.Error("healthy store reported invalid")
		}
		if err := ex.Update(func(tx *store.Tx) error {
			tx.DB().Articles["local/first.md"] = revision("local", "first.md", "# First")
			return nil
		}); err != nil {
			return err
		}
		if _, ok := ex.DB().Articles["local/first.md"]; !ok {
			t.Error("committed step is not visible to the next one")
		}
		if err := ex.Update(func(tx *store.Tx) error {
			tx.DB().Articles["local/second.md"] = revision("local", "second.md", "# Second")
			return refused
		}); !errors.Is(err, refused) {
			t.Error("failed step did not return its error", err)
		}
		if _, ok := ex.DB().Articles["local/second.md"]; ok {
			t.Error("failed step changed the committed state")
		}
		return refused
	})
	if !errors.Is(err, refused) {
		t.Fatal(err)
	}
	reopened, err := store.Open(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	db := storetest.Read(t, reopened)
	if _, ok := db.Articles["local/first.md"]; !ok {
		t.Fatal("earlier commit was lost when the exclusive section failed")
	}
	if _, ok := db.Articles["local/second.md"]; ok {
		t.Fatal("failed step reached disk")
	}
}
