package main

import (
	"bytes"
	"context"
	"github.com/Mesteriis/veda-wiki/internal/github"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func connectedMoveFixtures(t *testing.T) (*Store, *SyncManager, *githubtest.Fake, *githubtest.Fake) {
	t.Helper()
	s := testStore(t)
	one := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Source"})
	two := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Destination"})
	one.Modify(func() { one.Private = true })
	two.Modify(func() { two.Name = "org/second"; two.RepoID = 42; two.Private = true })
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		second := strings.HasPrefix(r.URL.Path, "/repos/org/second")
		if r.URL.Path == "/graphql" {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "bad request", 400)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			second = bytes.Contains(body, []byte(`"name":"second"`)) || bytes.Contains(body, []byte(`fixture-node-org/second`))
		}
		if second {
			two.Serve(w, r)
		} else {
			one.Serve(w, r)
		}
	}))
	t.Cleanup(gateway.Close)
	client := github.NewForTest(gateway.URL, gateway.Client())
	m := newSyncManager(s, client)
	if _, err := s.saveAccount(context.Background(), client, "operator", github.Secret{Kind: "token", Token: githubtest.Access}); err != nil {
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
	if original.Contents()["README.md"] != "# Source" || original.WriteCount() != 0 || s.moves()[0].State != "waiting_destination" {
		t.Fatal("source deleted before destination observation")
	}
	syncFixtureSource(t, manager, "two")
	if s.moves()[0].State != "waiting_source_delete" || destination.Contents()["moved.md"] != "# Source" {
		t.Fatal("destination not confirmed", s.moves())
	}
	syncFixtureSource(t, manager, "one")
	if s.moves()[0].State != "deleting_source" {
		t.Fatal("delete not prepared", s.moves())
	}
	syncFixtureSource(t, manager, "one")
	if s.moves()[0].State != "complete" || original.WriteCount() != 1 || destination.WriteCount() != 1 {
		t.Fatal("move not completed", s.moves())
	}
	if _, exists := original.Contents()["README.md"]; exists {
		t.Fatal("original still exists")
	}
	if destination.Contents()["moved.md"] != "# Source" {
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
			destination.Modify(func() { destination.Fail = true })
		} else {
			destination.Advance(map[string]string{"veda.yaml": metadataFixture, "README.md": "# Destination", "moved.md": "# External change"})
		}
		syncFixtureSource(t, manager, "one")
		if original.WriteCount() != 0 || original.Contents()["README.md"] != "# Source" {
			t.Fatal("source deleted without checking destination")
		}
	}
}

func TestMoveRejectsAliasedRepositoryAndSamePhysicalPath(t *testing.T) {
	s, _, original, _ := connectedMoveFixtures(t)
	// Model a stale second source view of the same repository and branch.
	s.Lock()
	db := s.Clone()
	alias := db.Connections["two"]
	alias.RepositoryID = db.Connections["one"].RepositoryID
	alias.Branch = db.Connections["one"].Branch
	db.Connections["two"] = alias
	delete(db.Articles, "two/README.md")
	err := s.Commit(db)
	s.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.moveArticle(moveRequest{SourceID: "one", SourcePath: "README.md", ExpectedRevision: 1, DestinationID: "two", DestinationPath: "README.md", ConfirmMove: true})
	if err != ErrConflict {
		t.Fatalf("aliased path accepted: %v", err)
	}
	if original.Contents()["README.md"] != "# Source" || original.WriteCount() != 0 {
		t.Fatal("original changed")
	}
}

func TestUnavailableDestinationDoesNotBlockOtherSourceMoves(t *testing.T) {
	s, manager, original, destination := connectedMoveFixtures(t)
	original.Advance(map[string]string{"veda.yaml": metadataFixture, "README.md": "# Source", "other.md": "# Other"})
	syncFixtureSource(t, manager, "one")
	source(t, s, "local", "private")
	first, err := s.moveArticle(moveRequest{SourceID: "one", SourcePath: "README.md", ExpectedRevision: 1, DestinationID: "two", DestinationPath: "moved.md", ConfirmMove: true})
	if err != nil {
		t.Fatal(err)
	}
	syncFixtureSource(t, manager, "two")
	syncFixtureSource(t, manager, "two")
	second, err := s.moveArticle(moveRequest{SourceID: "one", SourcePath: "other.md", ExpectedRevision: 1, DestinationID: "local", DestinationPath: "other.md", ConfirmMove: true})
	if err != nil {
		t.Fatal(err)
	}
	destination.Modify(func() { destination.Fail = true })
	syncFixtureSource(t, manager, "one")
	s.RLock()
	firstState, secondState := s.DB().Moves[first.ID].State, s.DB().Moves[second.ID].State
	s.RUnlock()
	if firstState != "waiting_source_delete" || secondState != "deleting_source" {
		t.Fatalf("unexpected move states: %s %s", firstState, secondState)
	}
	files := original.Contents()
	if files["README.md"] != "# Source" {
		t.Fatal("unavailable destination lost source")
	}
	if _, exists := files["other.md"]; exists {
		t.Fatal("unrelated move did not advance")
	}
}

func TestUnavailableMoveDestinationDoesNotBlockUnrelatedWrites(t *testing.T) {
	s, manager, original, destination := connectedMoveFixtures(t)
	_, err := s.moveArticle(moveRequest{SourceID: "one", SourcePath: "README.md", ExpectedRevision: 1, DestinationID: "two", DestinationPath: "moved.md", ConfirmMove: true})
	if err != nil {
		t.Fatal(err)
	}
	syncFixtureSource(t, manager, "two")
	syncFixtureSource(t, manager, "two")
	if _, err := s.put("one", "other.md", "# Independent", 0); err != nil {
		t.Fatal(err)
	}
	destination.Modify(func() { destination.Fail = true })
	syncFixtureSource(t, manager, "one")
	files := original.Contents()
	if files["README.md"] != "# Source" || files["other.md"] != "# Independent" {
		t.Fatal("move outage blocked unrelated write or deleted source")
	}
	if s.moves()[0].State != "waiting_source_delete" {
		t.Fatal("move unexpectedly advanced", s.moves())
	}
}

func TestUncertainDeleteRefreshesBranchBeforeAnotherMove(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# First", "other.md": "# Second"})
	manager := connectFixture(t, s, f, true)
	first, err := s.moveArticle(moveRequest{SourceID: "handbook", SourcePath: "README.md", ExpectedRevision: 1, DestinationID: "local", DestinationPath: "first.md", ConfirmMove: true})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.moveArticle(moveRequest{SourceID: "handbook", SourcePath: "other.md", ExpectedRevision: 1, DestinationID: "local", DestinationPath: "second.md", ConfirmMove: true})
	if err != nil {
		t.Fatal(err)
	}
	f.Modify(func() { f.LoseResponse = true })
	manager.Once(context.Background())
	s.RLock()
	firstState, secondState := s.DB().Moves[first.ID].State, s.DB().Moves[second.ID].State
	s.RUnlock()
	if firstState != "deleting_source" || secondState != "waiting_source_delete" || f.WriteCount() != 1 {
		t.Fatal("uncertain attempt did not stop same-head writes")
	}
	if err := s.retryConnection("handbook"); err != nil {
		t.Fatal(err)
	}
	manager.Once(context.Background())
	manager.Once(context.Background())
	s.RLock()
	firstState, secondState = s.DB().Moves[first.ID].State, s.DB().Moves[second.ID].State
	s.RUnlock()
	if firstState != "complete" || secondState != "complete" || f.WriteCount() != 2 {
		t.Fatalf("moves did not recover: %s %s", firstState, secondState)
	}
}

func TestConnectedSourceMoveWaitsForRemoteDeletion(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Source"})
	manager := connectFixture(t, s, f, true)
	move, err := s.moveArticle(moveRequest{SourceID: "handbook", SourcePath: "README.md", ExpectedRevision: 1, DestinationID: "local", DestinationPath: "copy.md", ConfirmMove: true})
	if err != nil || move.State != "waiting_source_delete" {
		t.Fatalf("move: %#v %v", move, err)
	}
	if _, err := s.article("handbook", "README.md", false); err != nil || f.Contents()["README.md"] != "# Source" {
		t.Fatal("source was deleted before remote deletion", err)
	}
	reopened, err := openStore(s.Dir())
	if err != nil || reopened.DB().Version != 3 || reopened.moves()[0].SourceRepositoryID != f.RepoID {
		t.Fatal("move state was not durable", err)
	}
	f.Advance(map[string]string{"veda.yaml": metadataFixture, "README.md": "# Edited externally"})
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
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Source"})
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
	if f.Contents()["README.md"] != "# Source" {
		t.Fatal("remote source deleted")
	}
}

func TestConnectedSourceMoveRejectsReadOnlyOrUnsyncedSource(t *testing.T) {
	for _, write := range []bool{false, true} {
		s := testStore(t)
		source(t, s, "local", "private")
		f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Source"})
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
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Source", "other.md": "# Retain"})
	manager := connectFixture(t, s, f, true)
	move, err := s.moveArticle(moveRequest{SourceID: "handbook", SourcePath: "README.md", ExpectedRevision: 1, DestinationID: "local", DestinationPath: "copy.md", ConfirmMove: true})
	if err != nil {
		t.Fatal(err)
	}
	f.Modify(func() { f.LoseResponse = true })
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
	reopened, err := openStore(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.retryConnection("handbook"); err != nil {
		t.Fatal(err)
	}
	manager = newSyncManager(reopened, f.Client())
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
	files := f.Contents()
	if _, exists := files["README.md"]; exists || files["other.md"] != "# Retain" || f.WriteCount() != 1 {
		t.Fatal("unexpected remote changes")
	}
}

func TestConnectedSourceMoveCanCancelAfterDefinitiveRejection(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Source"})
	manager := connectFixture(t, s, f, true)
	move, err := s.moveArticle(moveRequest{SourceID: "handbook", SourcePath: "README.md", ExpectedRevision: 1, DestinationID: "local", DestinationPath: "copy.md", ConfirmMove: true})
	if err != nil {
		t.Fatal(err)
	}
	f.Modify(func() { f.Reject = true })
	manager.Once(context.Background())
	if got := s.moves()[0]; got.State != "waiting_source_delete" || got.PreparedDeletion != "" {
		t.Fatal("definitive rejection left source locked", got)
	}
	if err := s.cancelMove(move.ID, true); err != nil {
		t.Fatal(err)
	}
	if f.Contents()["README.md"] != "# Source" || f.WriteCount() != 0 {
		t.Fatal("source changed after rejected update")
	}
}

func TestConnectedSourceMoveUncertainAttemptRemainsLockedAfterRejection(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Source"})
	manager := connectFixture(t, s, f, true)
	move, err := s.moveArticle(moveRequest{SourceID: "handbook", SourcePath: "README.md", ExpectedRevision: 1, DestinationID: "local", DestinationPath: "copy.md", ConfirmMove: true})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a prepared request whose outcome could not be determined after restart.
	s.Lock()
	db := s.Clone()
	move.State, move.PreparedDeletion = "deleting_source", strings.Repeat("a", 40)
	db.Moves[move.ID] = move
	err = s.Commit(db)
	s.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	f.Modify(func() { f.Reject = true })
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
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Existing"})
	m := connectFixture(t, s, f, true)
	req := moveRequest{SourceID: "local", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "moved.md", ConfirmMove: true, ConfirmPublication: true}
	move, e := s.moveArticle(req)
	if e != nil || move.State != "waiting_destination" {
		t.Fatal(move, e)
	}
	f.Modify(func() { f.Fail = true })
	m.Once(context.Background())
	if _, e = s.article("local", "a.md", false); e != nil {
		t.Fatal("source lost during outage", e)
	}
	f.Modify(func() { f.Fail = false })
	if e = s.retryConnection("handbook"); e != nil {
		t.Fatal(e)
	}
	m.Once(context.Background())
	if _, e = s.article("local", "a.md", false); e != nil {
		t.Fatal("source removed before observation", e)
	}
	s, e = openStore(s.Dir())
	if e != nil {
		t.Fatal("restart pending move", e)
	}
	m = newSyncManager(s, f.Client())
	m.Once(context.Background())
	if _, e = s.article("local", "a.md", false); e != ErrNotFound || s.moves()[0].State != "complete" || f.Contents()["moved.md"] != "# Local" {
		t.Fatal("move did not finish after observation", e, s.moves())
	}
	reopened, e := openStore(s.Dir())
	if e != nil || reopened.moves()[0].State != "complete" {
		t.Fatal("move not durable", e)
	}
}
func TestMoveConcurrentEditsKeepSource(t *testing.T) {
	for _, side := range []string{"source", "destination"} {
		s := testStore(t)
		source(t, s, "local", "private")
		s.put("local", "a.md", "# Local", 0)
		f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Existing"})
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
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Existing"})
	manager := connectFixture(t, s, f, false)
	req := moveRequest{SourceID: "local", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "moved.md", ConfirmMove: true, ConfirmPublication: true}
	if _, e := s.moveArticle(req); e != nil {
		t.Fatal(e)
	}
	manager.Once(context.Background())
	if _, e := s.article("local", "a.md", false); e != nil || s.moves()[0].State != "waiting_destination" || f.WriteCount() != 0 {
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
	if _, e = s.translation().Submit(translationRequest{SourceID: "program", Path: "docs/a.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Перевод"}); e != nil {
		t.Fatal(e)
	}
	req := moveRequest{SourceID: "program", SourcePath: "docs/a.md", ExpectedRevision: 1, DestinationID: "target", DestinationPath: "a.md", ConfirmMove: true}
	if _, e = s.moveArticle(req); e != ErrInvalid {
		t.Fatal("translated article moved without translation", e)
	}
	source(t, s, "local", "private")
	s.put("local", "a.md", "# Local", 0)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Existing"})
	connectFixture(t, s, f, true)
	req = moveRequest{SourceID: "local", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "moved.md", ConfirmMove: true, ConfirmPublication: true}
	if _, e = s.moveArticle(req); e != nil {
		t.Fatal(e)
	}
	db := s.Clone()
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
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Existing"})
	manager := connectFixture(t, s, f, true)
	req := moveRequest{SourceID: "local", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "moved.md", ConfirmMove: true, ConfirmPublication: true}
	if _, e := s.moveArticle(req); e != nil {
		t.Fatal(e)
	}
	f.Modify(func() { f.LoseResponse = true })
	manager.Once(context.Background())
	if f.WriteCount() != 1 {
		t.Fatal("remote fixture did not apply commit")
	}
	if _, e := s.article("local", "a.md", false); e != nil {
		t.Fatal("source removed on ambiguous response", e)
	}
	reopened, e := openStore(s.Dir())
	if e != nil {
		t.Fatal(e)
	}
	if e = reopened.retryConnection("handbook"); e != nil {
		t.Fatal(e)
	}
	manager = newSyncManager(reopened, f.Client())
	manager.Once(context.Background())
	if _, e = reopened.article("local", "a.md", false); e != ErrNotFound || reopened.moves()[0].State != "complete" || f.WriteCount() != 1 {
		t.Fatal("lost response recovery failed", e, reopened.moves())
	}
}

func TestCancelWaitingMoveRetainsBothCopies(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	s.put("local", "a.md", "# Source", 0)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Existing"})
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
	if _, e = s.article("local", "a.md", false); e != nil || s.moves()[0].State != "cancelled" || f.Contents()["moved.md"] != "# Source" {
		t.Fatal("cancel did not retain copies", e, s.moves())
	}
	reopened, e := openStore(s.Dir())
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
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Existing"})
	manager := connectFixture(t, s, f, true)
	req := moveRequest{SourceID: "program", SourcePath: "docs/a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "moved.md", ConfirmMove: true, ConfirmPublication: true}
	if _, e = s.moveArticle(req); e != nil {
		t.Fatal(e)
	}
	if _, e = s.translation().Submit(translationRequest{SourceID: "program", Path: "docs/a.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Перевод"}); e != nil {
		t.Fatal(e)
	}
	manager.Once(context.Background())
	manager.Once(context.Background())
	a, e := s.article("program", "docs/a.md", false)
	if e != nil || a.Translation == nil || s.moves()[0].State != "source_changed" {
		t.Fatal("translation was lost", e, s.moves())
	}
}
