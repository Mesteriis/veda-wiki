package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func connectedMoveFixtures(t *testing.T) (*Store, *SyncManager, *fakeGitHub, *fakeGitHub) {
	t.Helper()
	s := testStore(t)
	one := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Source"})
	two := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Destination"})
	one.modify(func() { one.private = true })
	two.modify(func() { two.name = "org/second"; two.repoID = 42; two.private = true })
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/repos/org/second") {
			two.serve(w, r)
		} else {
			one.serve(w, r)
		}
	}))
	t.Cleanup(gateway.Close)
	client := &GitHubClient{base: gateway.URL, http: gateway.Client()}
	m := newSyncManager(s, client)
	if _, err := s.saveAccount(context.Background(), client, "operator", GitHubSecret{Kind: "token", Token: fixtureAccess}); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ id, repo string }{{"one", "org/handbook"}, {"two", "org/second"}} {
		if _, err := m.connect(context.Background(), connectRequest{ID: item.id, Repository: item.repo, AccountID: "operator", Write: true}); err != nil {
			t.Fatal(err)
		}
	}
	m.Once(context.Background())
	return s, m, one, two
}

func syncFixtureSource(t *testing.T, m *SyncManager, id string) {
	t.Helper()
	for _, conn := range m.store.connections() {
		if conn.ID == id {
			m.syncSource(context.Background(), conn)
			return
		}
	}
	t.Fatalf("missing connection %s", id)
}

func TestMoveBetweenConnectedRepositoriesWaitsForObservation(t *testing.T) {
	s, manager, original, destination := connectedMoveFixtures(t)
	move, err := s.moveArticle(moveRequest{SourceID: "one", SourcePath: "README.md", ExpectedRevision: 1, DestinationID: "two", DestinationPath: "moved.md", ConfirmMove: true})
	if err != nil || move.State != "waiting_destination" {
		t.Fatalf("move: %#v %v", move, err)
	}
	syncFixtureSource(t, manager, "two")
	syncFixtureSource(t, manager, "one")
	if original.contents()["README.md"] != "# Source" || original.writeCount() != 0 || s.moves()[0].State != "waiting_destination" {
		t.Fatal("source deleted before destination observation")
	}
	syncFixtureSource(t, manager, "two")
	if s.moves()[0].State != "waiting_source_delete" || destination.contents()["moved.md"] != "# Source" {
		t.Fatal("destination not confirmed", s.moves())
	}
	syncFixtureSource(t, manager, "one")
	if s.moves()[0].State != "deleting_source" {
		t.Fatal("delete not prepared", s.moves())
	}
	syncFixtureSource(t, manager, "one")
	if s.moves()[0].State != "complete" || original.writeCount() != 1 || destination.writeCount() != 1 {
		t.Fatal("move not completed", s.moves())
	}
	if _, exists := original.contents()["README.md"]; exists {
		t.Fatal("original still exists")
	}
	if destination.contents()["moved.md"] != "# Source" {
		t.Fatal("destination lost")
	}
}

func TestMoveDoesNotDeleteSourceWhenDestinationUnavailableOrChanged(t *testing.T) {
	for _, outage := range []bool{true, false} {
		s, manager, original, destination := connectedMoveFixtures(t)
		_, err := s.moveArticle(moveRequest{SourceID: "one", SourcePath: "README.md", ExpectedRevision: 1, DestinationID: "two", DestinationPath: "moved.md", ConfirmMove: true})
		if err != nil {
			t.Fatal(err)
		}
		syncFixtureSource(t, manager, "two")
		syncFixtureSource(t, manager, "two")
		if s.moves()[0].State != "waiting_source_delete" {
			t.Fatal(s.moves())
		}
		if outage {
			destination.modify(func() { destination.fail = true })
		} else {
			destination.advance(map[string]string{"veda.yaml": metadataFixture, "README.md": "# Destination", "moved.md": "# External change"})
		}
		syncFixtureSource(t, manager, "one")
		if original.writeCount() != 0 || original.contents()["README.md"] != "# Source" {
			t.Fatal("source deleted without checking destination")
		}
	}
}

func TestMoveLocalAtomicAndRetainsHistory(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	source(t, s, "to", "private")
	if _, e := s.put("from", "a.md", "# Local", 0); e != nil {
		t.Fatal(e)
	}
	req := moveRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "a.md"}
	if _, e := s.moveArticle(req); e != ErrInvalid {
		t.Fatal("move without consent", e)
	}
	req.ConfirmMove = true
	m, e := s.moveArticle(req)
	if e != nil || m.State != "complete" {
		t.Fatal(m, e)
	}
	if _, e := s.article("from", "a.md", false); e != ErrNotFound {
		t.Fatal("source still visible", e)
	}
	target, e := s.article("to", "a.md", false)
	if e != nil || target.Versions[0].Text != "# Local" || target.CopiedFrom == nil {
		t.Fatal("destination missing", e)
	}
	reopened, e := openStore(s.dir)
	if e != nil || reopened.db.Version != 2 || !reopened.db.Articles["from/a.md"].Deleted || len(reopened.db.Articles["from/a.md"].Versions) != 1 || reopened.moves()[0].State != "complete" {
		t.Fatal("move history not durable", e)
	}
}

func TestConnectedSourceMoveWaitsForRemoteDeletion(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Source"})
	manager := connectFixture(t, s, f, true)
	move, err := s.moveArticle(moveRequest{SourceID: "handbook", SourcePath: "README.md", ExpectedRevision: 1, DestinationID: "local", DestinationPath: "copy.md", ConfirmMove: true})
	if err != nil || move.State != "waiting_source_delete" {
		t.Fatalf("move: %#v %v", move, err)
	}
	if _, err := s.article("handbook", "README.md", false); err != nil || f.contents()["README.md"] != "# Source" {
		t.Fatal("source was deleted before remote deletion", err)
	}
	reopened, err := openStore(s.dir)
	if err != nil || reopened.db.Version != 3 || reopened.moves()[0].SourceRepositoryID != f.repoID {
		t.Fatal("move state was not durable", err)
	}
	f.advance(map[string]string{"veda.yaml": metadataFixture, "README.md": "# Edited externally"})
	manager.Once(context.Background())
	if s.moves()[0].State != "source_changed" {
		t.Fatal("upstream edit did not block deletion", s.moves())
	}
	if got, err := s.article("handbook", "README.md", false); err != nil || got.Versions[len(got.Versions)-1].Text != "# Edited externally" {
		t.Fatal("upstream edit lost", err)
	}
}

func TestConnectedSourceMoveCancellationKeepsCopies(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Source"})
	connectFixture(t, s, f, true)
	move, err := s.moveArticle(moveRequest{SourceID: "handbook", SourcePath: "README.md", ExpectedRevision: 1, DestinationID: "local", DestinationPath: "copy.md", ConfirmMove: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.cancelMove(move.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.article("handbook", "README.md", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.article("local", "copy.md", false); err != nil {
		t.Fatal(err)
	}
	if f.contents()["README.md"] != "# Source" {
		t.Fatal("remote source deleted")
	}
}

func TestConnectedSourceMoveRejectsReadOnlyOrUnsyncedSource(t *testing.T) {
	for _, write := range []bool{false, true} {
		s := testStore(t)
		source(t, s, "local", "private")
		f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Source"})
		connectFixture(t, s, f, write)
		if write {
			if _, err := s.put("handbook", "README.md", "# Local edit", 1); err != nil {
				t.Fatal(err)
			}
		}
		expected := 1
		if write {
			expected = 2
		}
		_, err := s.moveArticle(moveRequest{SourceID: "handbook", SourcePath: "README.md", ExpectedRevision: expected, DestinationID: "local", DestinationPath: "copy.md", ConfirmMove: true})
		if err != ErrConflict {
			t.Fatalf("write=%v: expected conflict, got %v", write, err)
		}
		if _, err := s.article("local", "copy.md", false); err != ErrNotFound {
			t.Fatal("unexpected destination", err)
		}
	}
}

func TestConnectedSourceMoveRecoversLostDeleteResponse(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Source", "other.md": "# Retain"})
	manager := connectFixture(t, s, f, true)
	move, err := s.moveArticle(moveRequest{SourceID: "handbook", SourcePath: "README.md", ExpectedRevision: 1, DestinationID: "local", DestinationPath: "copy.md", ConfirmMove: true})
	if err != nil {
		t.Fatal(err)
	}
	f.modify(func() { f.loseResponse = true })
	manager.Once(context.Background())
	if s.moves()[0].State != "deleting_source" || s.moves()[0].PreparedDeletion == "" {
		t.Fatal("prepared delete not recorded", s.moves())
	}
	if _, err := s.article("handbook", "README.md", false); err != nil {
		t.Fatal("source hidden before observation", err)
	}
	if _, err := s.put("local", "copy.md", "# Changed", 1); err != ErrConflict {
		t.Fatal("destination not locked", err)
	}
	if err := s.cancelMove(move.ID, true); err != ErrConflict {
		t.Fatal("uncertain deletion was cancelled", err)
	}
	reopened, err := openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.retryConnection("handbook"); err != nil {
		t.Fatal(err)
	}
	manager = newSyncManager(reopened, f.client())
	manager.Once(context.Background())
	if reopened.moves()[0].State != "complete" {
		t.Fatal("deletion not observed", reopened.moves())
	}
	if _, err := reopened.article("handbook", "README.md", false); err != ErrNotFound {
		t.Fatal("source still present", err)
	}
	if got, err := reopened.article("local", "copy.md", false); err != nil || got.Versions[0].Text != "# Source" {
		t.Fatal("destination lost", err)
	}
	files := f.contents()
	if _, exists := files["README.md"]; exists || files["other.md"] != "# Retain" || f.writeCount() != 1 {
		t.Fatal("unexpected remote changes")
	}
}

func TestConnectedSourceMoveCanCancelAfterDefinitiveRejection(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Source"})
	manager := connectFixture(t, s, f, true)
	move, err := s.moveArticle(moveRequest{SourceID: "handbook", SourcePath: "README.md", ExpectedRevision: 1, DestinationID: "local", DestinationPath: "copy.md", ConfirmMove: true})
	if err != nil {
		t.Fatal(err)
	}
	f.modify(func() { f.reject = true })
	manager.Once(context.Background())
	if got := s.moves()[0]; got.State != "waiting_source_delete" || got.PreparedDeletion != "" {
		t.Fatal("definitive rejection left source locked", got)
	}
	if err := s.cancelMove(move.ID, true); err != nil {
		t.Fatal(err)
	}
	if f.contents()["README.md"] != "# Source" || f.writeCount() != 0 {
		t.Fatal("source changed after rejected update")
	}
}

func TestConnectedSourceMoveUncertainAttemptRemainsLockedAfterRejection(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Source"})
	manager := connectFixture(t, s, f, true)
	move, err := s.moveArticle(moveRequest{SourceID: "handbook", SourcePath: "README.md", ExpectedRevision: 1, DestinationID: "local", DestinationPath: "copy.md", ConfirmMove: true})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a prepared request whose outcome could not be determined after restart.
	s.mu.Lock()
	db := s.clone()
	move.State, move.PreparedDeletion = "deleting_source", strings.Repeat("a", 40)
	db.Moves[move.ID] = move
	err = s.commit(db)
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	f.modify(func() { f.reject = true })
	manager.Once(context.Background())
	if s.moves()[0].State != "deleting_source" {
		t.Fatal("uncertain prior request was unlocked", s.moves())
	}
	if err := s.cancelMove(move.ID, true); err != ErrConflict {
		t.Fatal("uncertain move cancelled", err)
	}
}
func TestMoveWaitsForObservedRemoteDestination(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	s.put("local", "a.md", "# Local", 0)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Existing"})
	m := connectFixture(t, s, f, true)
	req := moveRequest{SourceID: "local", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "moved.md", ConfirmMove: true, ConfirmPublication: true}
	move, e := s.moveArticle(req)
	if e != nil || move.State != "waiting_destination" {
		t.Fatal(move, e)
	}
	f.modify(func() { f.fail = true })
	m.Once(context.Background())
	if _, e = s.article("local", "a.md", false); e != nil {
		t.Fatal("source lost during outage", e)
	}
	f.modify(func() { f.fail = false })
	if e = s.retryConnection("handbook"); e != nil {
		t.Fatal(e)
	}
	m.Once(context.Background())
	if _, e = s.article("local", "a.md", false); e != nil {
		t.Fatal("source removed before observation", e)
	}
	s, e = openStore(s.dir)
	if e != nil {
		t.Fatal("restart pending move", e)
	}
	m = newSyncManager(s, f.client())
	m.Once(context.Background())
	if _, e = s.article("local", "a.md", false); e != ErrNotFound || s.moves()[0].State != "complete" || f.contents()["moved.md"] != "# Local" {
		t.Fatal("move did not finish after observation", e, s.moves())
	}
	reopened, e := openStore(s.dir)
	if e != nil || reopened.moves()[0].State != "complete" {
		t.Fatal("move not durable", e)
	}
}
func TestMoveConcurrentEditsKeepSource(t *testing.T) {
	for _, side := range []string{"source", "destination"} {
		s := testStore(t)
		source(t, s, "local", "private")
		s.put("local", "a.md", "# Local", 0)
		f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Existing"})
		m := connectFixture(t, s, f, true)
		req := moveRequest{SourceID: "local", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "moved.md", ConfirmMove: true, ConfirmPublication: true}
		if _, e := s.moveArticle(req); e != nil {
			t.Fatal(e)
		}
		if side == "source" {
			if _, e := s.put("local", "a.md", "# Edited source", 1); e != nil {
				t.Fatal(e)
			}
		} else {
			if _, e := s.put("handbook", "moved.md", "# Edited destination", 1); e != nil {
				t.Fatal(e)
			}
		}
		m.Once(context.Background())
		m.Once(context.Background())
		if _, e := s.article("local", "a.md", false); e != nil || s.moves()[0].State != side+"_changed" {
			t.Fatal("concurrent edit lost source", side, e, s.moves())
		}
	}
}
func TestMoveRejectsImportedSourcesAndRelativeDependencies(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	source(t, s, "target", "private")
	for _, item := range []struct{ path, text string }{{"image.md", "# Doc\n![image](assets/a.png)"}, {"link.md", "# Doc\n[guide](../guide.md)"}} {
		if _, e := s.put("local", item.path, item.text, 0); e != nil {
			t.Fatal(e)
		}
		req := moveRequest{SourceID: "local", SourcePath: item.path, ExpectedRevision: 1, DestinationID: "target", DestinationPath: item.path, ConfirmMove: true}
		if _, e := s.moveArticle(req); e != ErrInvalid {
			t.Fatal("relative dependency accepted", e)
		}
	}
	batch := testBatch()
	if _, e := s.importBatch(batch); e != nil {
		t.Fatal(e)
	}
	req := moveRequest{SourceID: "handbook", SourcePath: "README.md", ExpectedRevision: 1, DestinationID: "target", DestinationPath: "x.md", ConfirmMove: true}
	if _, e := s.moveArticle(req); e != ErrInvalid {
		t.Fatal("imported source accepted", e)
	}
	w := Wiki{Store: s, Token: "test-owner"}
	if r := request(w, "GET", "/api/v1/moves", "", false); r.Code != 404 {
		t.Fatal("move metadata exposed", r.Code)
	}
}

func TestMoveReadOnlyDestinationRetainsSource(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	if _, e := s.put("local", "a.md", "# Source", 0); e != nil {
		t.Fatal(e)
	}
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Existing"})
	manager := connectFixture(t, s, f, false)
	req := moveRequest{SourceID: "local", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "moved.md", ConfirmMove: true, ConfirmPublication: true}
	if _, e := s.moveArticle(req); e != nil {
		t.Fatal(e)
	}
	manager.Once(context.Background())
	if _, e := s.article("local", "a.md", false); e != nil || s.moves()[0].State != "waiting_destination" || f.writeCount() != 0 {
		t.Fatal("read-only destination removed source", e)
	}
}

func TestMoveRejectsTranslationsAndDestinationIdentityChange(t *testing.T) {
	s := testStore(t)
	if e := s.addSource(Source{ID: "program", Title: "Program", Kind: "program", Visibility: "private"}); e != nil {
		t.Fatal(e)
	}
	source(t, s, "target", "private")
	rev, e := s.put("program", "docs/a.md", "# Original", 0)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.submitTranslation(translationRequest{SourceID: "program", Path: "docs/a.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Перевод"}); e != nil {
		t.Fatal(e)
	}
	req := moveRequest{SourceID: "program", SourcePath: "docs/a.md", ExpectedRevision: 1, DestinationID: "target", DestinationPath: "a.md", ConfirmMove: true}
	if _, e = s.moveArticle(req); e != ErrInvalid {
		t.Fatal("translated article moved without translation", e)
	}
	source(t, s, "local", "private")
	s.put("local", "a.md", "# Local", 0)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Existing"})
	connectFixture(t, s, f, true)
	req = moveRequest{SourceID: "local", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "moved.md", ConfirmMove: true, ConfirmPublication: true}
	if _, e = s.moveArticle(req); e != nil {
		t.Fatal(e)
	}
	db := s.clone()
	dst := db.Sources["handbook"]
	dst.ConnectionID = ""
	db.Sources[dst.ID] = dst
	reconcileMoves(&db)
	for _, m := range db.Moves {
		if m.State != "destination_changed" {
			t.Fatal("changed destination identity accepted", m)
		}
	}
	if db.Articles["local/a.md"].Deleted {
		t.Fatal("source removed after destination identity changed")
	}
}

func TestMoveRecoversLostRemoteResponseBeforeRemovingSource(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	if _, e := s.put("local", "a.md", "# Source", 0); e != nil {
		t.Fatal(e)
	}
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Existing"})
	manager := connectFixture(t, s, f, true)
	req := moveRequest{SourceID: "local", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "moved.md", ConfirmMove: true, ConfirmPublication: true}
	if _, e := s.moveArticle(req); e != nil {
		t.Fatal(e)
	}
	f.modify(func() { f.loseResponse = true })
	manager.Once(context.Background())
	if f.writeCount() != 1 {
		t.Fatal("remote fixture did not apply commit")
	}
	if _, e := s.article("local", "a.md", false); e != nil {
		t.Fatal("source removed on ambiguous response", e)
	}
	reopened, e := openStore(s.dir)
	if e != nil {
		t.Fatal(e)
	}
	if e = reopened.retryConnection("handbook"); e != nil {
		t.Fatal(e)
	}
	manager = newSyncManager(reopened, f.client())
	manager.Once(context.Background())
	if _, e = reopened.article("local", "a.md", false); e != ErrNotFound || reopened.moves()[0].State != "complete" || f.writeCount() != 1 {
		t.Fatal("lost response recovery failed", e, reopened.moves())
	}
}

func TestCancelWaitingMoveRetainsBothCopies(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	s.put("local", "a.md", "# Source", 0)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Existing"})
	manager := connectFixture(t, s, f, true)
	req := moveRequest{SourceID: "local", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "moved.md", ConfirmMove: true, ConfirmPublication: true}
	m, e := s.moveArticle(req)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.cancelMove(m.ID, false); e != ErrInvalid {
		t.Fatal("cancel without confirmation", e)
	}
	if e = s.cancelMove(m.ID, true); e != nil {
		t.Fatal(e)
	}
	manager.Once(context.Background())
	manager.Once(context.Background())
	if _, e = s.article("local", "a.md", false); e != nil || s.moves()[0].State != "cancelled" || f.contents()["moved.md"] != "# Source" {
		t.Fatal("cancel did not retain copies", e, s.moves())
	}
	reopened, e := openStore(s.dir)
	if e != nil || reopened.moves()[0].State != "cancelled" {
		t.Fatal("cancel not durable", e)
	}
}

func TestMoveKeepsSourceWhenTranslationIsAddedWhileWaiting(t *testing.T) {
	s := testStore(t)
	if e := s.addSource(Source{ID: "program", Title: "Program", Kind: "program", Visibility: "private"}); e != nil {
		t.Fatal(e)
	}
	rev, e := s.put("program", "docs/a.md", "# Original", 0)
	if e != nil {
		t.Fatal(e)
	}
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Existing"})
	manager := connectFixture(t, s, f, true)
	req := moveRequest{SourceID: "program", SourcePath: "docs/a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "moved.md", ConfirmMove: true, ConfirmPublication: true}
	if _, e = s.moveArticle(req); e != nil {
		t.Fatal(e)
	}
	if _, e = s.submitTranslation(translationRequest{SourceID: "program", Path: "docs/a.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Перевод"}); e != nil {
		t.Fatal(e)
	}
	manager.Once(context.Background())
	manager.Once(context.Background())
	a, e := s.article("program", "docs/a.md", false)
	if e != nil || a.Translation == nil || s.moves()[0].State != "source_changed" {
		t.Fatal("translation was lost", e, s.moves())
	}
}

func TestMovePrivateToPublicRequiresSeparatePublicationConsent(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	source(t, s, "public", "public")
	s.put("local", "a.md", "# Private", 0)
	req := moveRequest{SourceID: "local", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "public", DestinationPath: "a.md", ConfirmMove: true}
	if _, e := s.moveArticle(req); e != ErrInvalid {
		t.Fatal("publication without consent accepted", e)
	}
	if _, e := s.article("local", "a.md", false); e != nil {
		t.Fatal("source lost on refusal", e)
	}
	req.ConfirmPublication = true
	if m, e := s.moveArticle(req); e != nil || m.State != "complete" {
		t.Fatal("approved move failed", m, e)
	}
	if _, e := s.article("public", "a.md", true); e != nil {
		t.Fatal("approved destination not visible", e)
	}
}
