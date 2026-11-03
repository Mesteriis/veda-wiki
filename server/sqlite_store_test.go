package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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

func TestSQLiteArticleAndOutboxCommitAtomically(t *testing.T) {
	s := testStore(t)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "# Original"})
	connectFixture(t, s, f, true)
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
	if _, err = db.Exec(`CREATE TRIGGER reject_job BEFORE INSERT ON records WHEN NEW.category='job' BEGIN SELECT RAISE(ABORT, 'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err = s.put("handbook", "guide.md", "# Updated", 1); err == nil {
		t.Fatal("job failure accepted")
	}
	article, err := s.article("handbook", "guide.md", false)
	if err != nil || len(article.Versions) != 1 || article.Versions[0].Text != "# Original" || len(s.jobs()) != 0 {
		t.Fatal("failed transaction changed article or outbox", err)
	}
	reopened, err := openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	article, err = reopened.article("handbook", "guide.md", false)
	if err != nil || len(article.Versions) != 1 || len(reopened.jobs()) != 0 {
		t.Fatal("failed transaction survived restart", err)
	}
	db, err = sqliteDB(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("DROP TRIGGER reject_job"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err = s.put("handbook", "guide.md", "# Updated", 1); err != nil {
		t.Fatal(err)
	}
	reopened, err = openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	article, err = reopened.article("handbook", "guide.md", false)
	if err != nil || len(article.Versions) != 2 || len(reopened.jobs()) != 1 || reopened.jobs()[0].SHA != article.Versions[1].SHA {
		t.Fatal("successful article and job did not persist", err)
	}
	liveJSON, liveErr := json.Marshal(s.db)
	restoredJSON, restoredErr := json.Marshal(reopened.db)
	if liveErr != nil || restoredErr != nil || string(liveJSON) != string(restoredJSON) {
		t.Fatal("in-memory and restored SQLite state differ")
	}
	if _, err = s.put("handbook", "guide.md", "# Updated again", 2); err != nil {
		t.Fatal(err)
	}
	reopened, err = openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	states := map[int]string{}
	for _, job := range reopened.jobs() {
		states[job.Revision] = job.State
	}
	if states[2] != "superseded" || states[3] != "pending" {
		t.Fatal("outbox supersession not durable")
	}
}

func TestSQLiteArticleUpdatePersistsMoveReconciliation(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Source"})
	connectFixture(t, s, f, true)
	move, err := s.moveArticle(moveRequest{SourceID: "handbook", SourcePath: "README.md", ExpectedRevision: 1, DestinationID: "local", DestinationPath: "copy.md", ConfirmMove: true})
	if err != nil || move.State != "waiting_source_delete" {
		t.Fatal("fixture move not pending", err)
	}
	if err = migrateSQLite(s.dir); err != nil {
		t.Fatal(err)
	}
	s, err = openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.put("local", "copy.md", "# Changed destination", 1); err != nil {
		t.Fatal(err)
	}
	reopened, err := openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	a, err := reopened.article("local", "copy.md", false)
	if err != nil || len(a.Versions) != 2 || reopened.moves()[0].State != "destination_changed" {
		t.Fatal("article and move state not durable", err)
	}
}

func TestSQLiteArticleUpdatePersistsOtherArticleWhenMoveCompletes(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	if _, err := s.put("local", "a.md", "# Source", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("local", "other.md", "# Other", 0); err != nil {
		t.Fatal(err)
	}
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Existing"})
	connectFixture(t, s, f, true)
	move, err := s.moveArticle(moveRequest{SourceID: "local", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "moved.md", ConfirmMove: true, ConfirmPublication: true})
	if err != nil || move.State != "waiting_destination" {
		t.Fatal("fixture move not pending", err)
	}
	if err = migrateSQLite(s.dir); err != nil {
		t.Fatal(err)
	}
	s, err = openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	db := s.clone()
	jobID := "handbook/moved.md@1"
	job := db.Outbox[jobID]
	job.State = "complete"
	db.Outbox[jobID] = job
	err = s.commit(db)
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.put("local", "other.md", "# Changed", 1); err != nil {
		t.Fatal(err)
	}
	reopened, err := openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = reopened.article("local", "a.md", false); err != ErrNotFound || reopened.moves()[0].State != "complete" {
		t.Fatal("completed move did not persist source deletion", err)
	}
	other, err := reopened.article("local", "other.md", false)
	if err != nil || len(other.Versions) != 2 {
		t.Fatal("unrelated update was not durable", err)
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

func TestSQLiteLostGitHubResponseRecoversWithoutDuplicatePush(t *testing.T) {
	s := testStore(t)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Original"})
	connectFixture(t, s, f, true)
	if err := migrateSQLite(s.dir); err != nil {
		t.Fatal(err)
	}
	s, err := openStore(s.dir)
	if err != nil || !s.sqlite {
		t.Fatal("SQLite reopen failed", err)
	}
	if _, err = s.put("handbook", "README.md", "# Local update", 1); err != nil {
		t.Fatal(err)
	}
	before, err := openStore(s.dir)
	if err != nil || len(before.jobs()) != 1 || before.jobs()[0].State != "pending" {
		t.Fatal("article and queued write not durable", err)
	}
	if a, err := before.article("handbook", "README.md", false); err != nil || a.Versions[len(a.Versions)-1].Text != "# Local update" {
		t.Fatal("local revision missing", err)
	}
	f.modify(func() { f.loseResponse = true })
	newSyncManager(s, f.client()).Once(context.Background())
	if f.writeCount() != 1 || len(s.jobs()) != 1 || s.jobs()[0].PreparedCommit == "" {
		t.Fatal("uncertain push not recorded")
	}
	restored, err := openStore(s.dir)
	if err != nil || restored.jobs()[0].PreparedCommit == "" {
		t.Fatal("uncertain state lost after restart", err)
	}
	if err = restored.retryConnection("handbook"); err != nil {
		t.Fatal(err)
	}
	newSyncManager(restored, f.client()).Once(context.Background())
	if f.writeCount() != 1 || restored.jobs()[0].State != "complete" || f.contents()["README.md"] != "# Local update" {
		t.Fatal("recovery duplicated or lost update")
	}
	final, err := openStore(s.dir)
	if err != nil || final.jobs()[0].State != "complete" {
		t.Fatal("recovery not durable", err)
	}
}

func TestSQLiteLostResponseKeepsSecondRepositoryIndependent(t *testing.T) {
	s := testStore(t)
	first := newFakeGitHub(t, map[string]string{"docs/guide.md": "# First original"})
	second := newFakeGitHub(t, map[string]string{"docs/guide.md": "# Second original"})
	second.modify(func() { second.name = "org/second"; second.repoID = 42; second.private = true })
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/repos/org/second") {
			second.serve(w, r)
		} else {
			first.serve(w, r)
		}
	}))
	defer gateway.Close()
	client := &GitHubClient{base: gateway.URL, http: gateway.Client()}
	m := newSyncManager(s, client)
	if _, err := s.saveAccount(context.Background(), client, "operator", GitHubSecret{Kind: "token", Token: fixtureAccess}); err != nil {
		t.Fatal(err)
	}
	for _, repo := range []struct{ id, name string }{{"one", "org/handbook"}, {"two", "org/second"}} {
		if _, err := m.connect(context.Background(), connectRequest{ID: repo.id, Repository: repo.name, AccountID: "operator", Write: true}); err != nil {
			t.Fatal(err)
		}
	}
	m.Once(context.Background())
	if err := migrateSQLite(s.dir); err != nil {
		t.Fatal(err)
	}
	s, err := openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.put("one", "docs/guide.md", "# First updated", 1); err != nil {
		t.Fatal(err)
	}
	first.modify(func() { first.loseResponse = true })
	newSyncManager(s, client).Once(context.Background())
	if first.writeCount() != 1 || second.writeCount() != 0 || len(s.jobs()) != 1 || s.jobs()[0].PreparedCommit == "" {
		t.Fatal("first write did not enter recoverable state")
	}
	reopened, err := openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = reopened.retryConnection("one"); err != nil {
		t.Fatal(err)
	}
	newSyncManager(reopened, client).Once(context.Background())
	if first.writeCount() != 1 || second.writeCount() != 0 || reopened.jobs()[0].State != "complete" {
		t.Fatal("unexpected duplicate or second repository write")
	}
	if first.contents()["docs/guide.md"] != "# First updated" || second.contents()["docs/guide.md"] != "# Second original" {
		t.Fatal("repository contents crossed")
	}
	a, err := reopened.article("two", "docs/guide.md", false)
	if err != nil || len(a.Versions) != 1 || a.Versions[0].Text != "# Second original" {
		t.Fatal("second local article changed", err)
	}
}
