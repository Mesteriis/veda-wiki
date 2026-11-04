package syncer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/github"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
	"github.com/Mesteriis/veda-wiki/internal/store"
	"github.com/Mesteriis/veda-wiki/internal/store/storetest"
	"github.com/Mesteriis/veda-wiki/internal/transfer"
)

func TestSQLiteMigrationRestoresPendingSync(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Original"})
	connectFixture(t, s, f, true)
	if _, err := s.put("handbook", "README.md", "# Local update", 1); err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateSQLite(s.Dir()); err != nil {
		t.Fatal(err)
	}
	s, err := openStore(t, s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	m := newSyncManager(s, f.Client())
	m.Once(context.Background())
	m.Once(context.Background())
	if f.WriteCount() != 1 || len(s.jobs()) != 1 || s.jobs()[0].State != "complete" {
		t.Fatal("pending sync not resumed", s.jobs())
	}
	reopened, err := openStore(t, s.Dir())
	if err != nil || reopened.jobs()[0].State != "complete" {
		t.Fatal("completed sync not durable", err)
	}
}

func TestSQLiteArticleAndOutboxCommitAtomically(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "# Original"})
	connectFixture(t, s, f, true)
	if err := store.MigrateSQLite(s.Dir()); err != nil {
		t.Fatal(err)
	}
	s, err := openStore(t, s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	db := storetest.SQLite(t, s.Dir())
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
	reopened, err := openStore(t, s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	article, err = reopened.article("handbook", "guide.md", false)
	if err != nil || len(article.Versions) != 1 || len(reopened.jobs()) != 0 {
		t.Fatal("failed transaction survived restart", err)
	}
	db = storetest.SQLite(t, s.Dir())
	if _, err = db.Exec("DROP TRIGGER reject_job"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err = s.put("handbook", "guide.md", "# Updated", 1); err != nil {
		t.Fatal(err)
	}
	reopened, err = openStore(t, s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	article, err = reopened.article("handbook", "guide.md", false)
	if err != nil || len(article.Versions) != 2 || len(reopened.jobs()) != 1 || reopened.jobs()[0].SHA != article.Versions[1].SHA {
		t.Fatal("successful article and job did not persist", err)
	}
	liveJSON, liveErr := json.Marshal(s.db())
	restoredJSON, restoredErr := json.Marshal(reopened.db())
	if liveErr != nil || restoredErr != nil || string(liveJSON) != string(restoredJSON) {
		t.Fatal("in-memory and restored SQLite state differ")
	}
	if _, err = s.put("handbook", "guide.md", "# Updated again", 2); err != nil {
		t.Fatal(err)
	}
	reopened, err = openStore(t, s.Dir())
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
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Source"})
	connectFixture(t, s, f, true)
	move, err := s.moveArticle(transfer.MoveRequest{SourceID: "handbook", SourcePath: "README.md", ExpectedRevision: 1, DestinationID: "local", DestinationPath: "copy.md", ConfirmMove: true})
	if err != nil || move.State != "waiting_source_delete" {
		t.Fatal("fixture move not pending", err)
	}
	if err = store.MigrateSQLite(s.Dir()); err != nil {
		t.Fatal(err)
	}
	s, err = openStore(t, s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.put("local", "copy.md", "# Changed destination", 1); err != nil {
		t.Fatal(err)
	}
	reopened, err := openStore(t, s.Dir())
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
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Existing"})
	connectFixture(t, s, f, true)
	move, err := s.moveArticle(transfer.MoveRequest{SourceID: "local", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "moved.md", ConfirmMove: true, ConfirmPublication: true})
	if err != nil || move.State != "waiting_destination" {
		t.Fatal("fixture move not pending", err)
	}
	if err = store.MigrateSQLite(s.Dir()); err != nil {
		t.Fatal(err)
	}
	s, err = openStore(t, s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	s.mutate(func(db *domain.Database) {
		jobID := "handbook/moved.md@1"
		job := db.Outbox[jobID]
		job.State = "complete"
		db.Outbox[jobID] = job
	})
	if _, err = s.put("local", "other.md", "# Changed", 1); err != nil {
		t.Fatal(err)
	}
	reopened, err := openStore(t, s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = reopened.article("local", "a.md", false); err != domain.ErrNotFound || reopened.moves()[0].State != "complete" {
		t.Fatal("completed move did not persist source deletion", err)
	}
	other, err := reopened.article("local", "other.md", false)
	if err != nil || len(other.Versions) != 2 {
		t.Fatal("unrelated update was not durable", err)
	}
}

func TestSQLiteLostGitHubResponseRecoversWithoutDuplicatePush(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Original"})
	connectFixture(t, s, f, true)
	if err := store.MigrateSQLite(s.Dir()); err != nil {
		t.Fatal(err)
	}
	s, err := openStore(t, s.Dir())
	if err != nil || !s.st.IsSQLite() {
		t.Fatal("SQLite reopen failed", err)
	}
	if _, err = s.put("handbook", "README.md", "# Local update", 1); err != nil {
		t.Fatal(err)
	}
	before, err := openStore(t, s.Dir())
	if err != nil || len(before.jobs()) != 1 || before.jobs()[0].State != "pending" {
		t.Fatal("article and queued write not durable", err)
	}
	if a, err := before.article("handbook", "README.md", false); err != nil || a.Versions[len(a.Versions)-1].Text != "# Local update" {
		t.Fatal("local revision missing", err)
	}
	f.Modify(func() { f.LoseResponse = true })
	newSyncManager(s, f.Client()).Once(context.Background())
	if f.WriteCount() != 1 || len(s.jobs()) != 1 || s.jobs()[0].PreparedCommit == "" {
		t.Fatal("uncertain push not recorded")
	}
	restored, err := openStore(t, s.Dir())
	if err != nil || restored.jobs()[0].PreparedCommit == "" {
		t.Fatal("uncertain state lost after restart", err)
	}
	if err = restored.retryConnection("handbook"); err != nil {
		t.Fatal(err)
	}
	newSyncManager(restored, f.Client()).Once(context.Background())
	if f.WriteCount() != 1 || restored.jobs()[0].State != "complete" || f.Contents()["README.md"] != "# Local update" {
		t.Fatal("recovery duplicated or lost update")
	}
	final, err := openStore(t, s.Dir())
	if err != nil || final.jobs()[0].State != "complete" {
		t.Fatal("recovery not durable", err)
	}
}

func TestSQLiteLostResponseKeepsSecondRepositoryIndependent(t *testing.T) {
	s := testStore(t)
	first := githubtest.New(t, map[string]string{"docs/guide.md": "# First original"})
	second := githubtest.New(t, map[string]string{"docs/guide.md": "# Second original"})
	second.Modify(func() { second.Name = "org/second"; second.RepoID = 42; second.Private = true })
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/repos/org/second") {
			second.Serve(w, r)
		} else {
			first.Serve(w, r)
		}
	}))
	defer gateway.Close()
	client := github.NewForTest(gateway.URL, gateway.Client())
	m := newSyncManager(s, client)
	if _, err := s.saveAccount(context.Background(), client, "operator", github.Secret{Kind: "token", Token: githubtest.Access}); err != nil {
		t.Fatal(err)
	}
	for _, repo := range []struct{ id, name string }{{"one", "org/handbook"}, {"two", "org/second"}} {
		if _, err := m.Connect(context.Background(), ConnectRequest{ID: repo.id, Repository: repo.name, AccountID: "operator", Write: true}); err != nil {
			t.Fatal(err)
		}
	}
	m.Once(context.Background())
	if err := store.MigrateSQLite(s.Dir()); err != nil {
		t.Fatal(err)
	}
	s, err := openStore(t, s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.put("one", "docs/guide.md", "# First updated", 1); err != nil {
		t.Fatal(err)
	}
	first.Modify(func() { first.LoseResponse = true })
	newSyncManager(s, client).Once(context.Background())
	if first.WriteCount() != 1 || second.WriteCount() != 0 || len(s.jobs()) != 1 || s.jobs()[0].PreparedCommit == "" {
		t.Fatal("first write did not enter recoverable state")
	}
	reopened, err := openStore(t, s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if err = reopened.retryConnection("one"); err != nil {
		t.Fatal(err)
	}
	newSyncManager(reopened, client).Once(context.Background())
	if first.WriteCount() != 1 || second.WriteCount() != 0 || reopened.jobs()[0].State != "complete" {
		t.Fatal("unexpected duplicate or second repository write")
	}
	if first.Contents()["docs/guide.md"] != "# First updated" || second.Contents()["docs/guide.md"] != "# Second original" {
		t.Fatal("repository contents crossed")
	}
	a, err := reopened.article("two", "docs/guide.md", false)
	if err != nil || len(a.Versions) != 1 || a.Versions[0].Text != "# Second original" {
		t.Fatal("second local article changed", err)
	}
}
