package syncer

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
	"github.com/Mesteriis/veda-wiki/internal/store"
	"github.com/Mesteriis/veda-wiki/internal/store/storetest"
)

// Sync status changes run twice per connection and cycle. On SQLite they must
// persist only connection records, not reserialize every article's history.
func TestSQLiteConnectionStatusWritesOnlyConnections(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Original"})
	connectFixture(t, s, f, false)
	if err := store.MigrateSQLite(s.Dir()); err != nil {
		t.Fatal(err)
	}
	s, err := s.reopen()
	if err != nil {
		t.Fatal(err)
	}
	// A record changed on disk behind the open store reveals whether article
	// rows are rewritten from memory.
	a := s.db().Articles["handbook/README.md"]
	a.Versions = append([]domain.Revision(nil), a.Versions...)
	a.Versions[0].Text = "# Changed on disk"
	a.Versions[0].SHA = domain.Digest([]byte(a.Versions[0].Text))
	// A v21 store keeps the text in the revision row and the article
	// record holds the header without it.
	row, err := json.Marshal(a.Versions[0])
	if err != nil {
		t.Fatal(err)
	}
	a.Versions[0].Text = ""
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	db := storetest.SQLite(t, s.Dir())
	if _, err = db.Exec(`UPDATE records SET data=? WHERE category='article' AND id='handbook/README.md'`, data); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE revisions SET data=? WHERE source_id='handbook' AND path='README.md' AND number=?`, row, a.Versions[0].Number); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if err = s.connectionStatusFor(s.connections()[0], "syncing", "verified", "", nil, time.Time{}); err != nil {
		t.Fatal(err)
	}
	reopened, err := s.reopen()
	if err != nil {
		t.Fatal(err)
	}
	if reopened.connections()[0].State != "syncing" {
		t.Fatal("connection status was not persisted")
	}
	if text := reopened.db().Articles["handbook/README.md"].Versions[0].Text; text != "# Changed on disk" {
		t.Fatal("status update rewrote article records", text)
	}
}

// Split from cmd/veda TestGitHubObservationTracksImportedOrVerifiedHead: a
// stale request cannot confirm the imported head.
func TestStaleConnectionCannotConfirmObservedHead(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "# Initial"})
	m := connectFixture(t, s, f, false)
	observed := s.observedAt("handbook")
	if observed.IsZero() {
		t.Fatal("successful GitHub import not observed")
	}
	conn := s.connections()[0]
	if err := s.pauseConnection("handbook", true); err != nil {
		t.Fatal(err)
	}
	if err := m.observeGitHubHead(conn, conn.LastPull, conn.TreeSHA); err != domain.ErrConflict {
		t.Fatal("stale connection advanced observation", err)
	}
	if !s.observedAt("handbook").Equal(observed) {
		t.Fatal("stale observation changed timestamp")
	}
}

// Split from cmd/veda TestMirrorConfigurationGuardsAndOwnerAPI: a worker
// that still holds the enabled revision cannot reconcile a paused mirror.
func TestPausedMirrorRejectsStaleWorker(t *testing.T) {
	s, _, _ := mirrorFixture(t)
	enabled, paused := true, false
	expected := 0
	if _, err := s.configureMirror(context.Background(), MirrorRequest{SourceID: "local", DestinationID: "handbook", Enabled: &enabled, ExpectedRevision: &expected}); err != nil {
		t.Fatal(err)
	}
	expected = 1
	if _, err := s.configureMirror(context.Background(), MirrorRequest{SourceID: "local", DestinationID: "handbook", Enabled: &paused, ExpectedRevision: &expected}); err != nil {
		t.Fatal(err)
	}
	old := 1
	if _, err := s.reconcileSourcesExpected(context.Background(), ReconcileRequest{SourceID: "local", DestinationID: "handbook"}, &old); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("paused stale worker accepted", err)
	}
}
