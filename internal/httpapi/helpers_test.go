package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/agentctx"
	"github.com/Mesteriis/veda-wiki/internal/auth"
	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/github"
	"github.com/Mesteriis/veda-wiki/internal/importer"
	"github.com/Mesteriis/veda-wiki/internal/store"
	"github.com/Mesteriis/veda-wiki/internal/store/storetest"
	"github.com/Mesteriis/veda-wiki/internal/syncer"
	"github.com/Mesteriis/veda-wiki/internal/transfer"
)

// Store is the request data view the tests prepare and inspect.
type Store = dataStore

// Wiki describes one listener the way the tests configure it; ServeHTTP
// serves each request through New with the equivalent Config and Deps.
type Wiki struct {
	AllowedHosts []string

	Public            bool
	Store             *Store
	Token             string
	ReaderToken       string
	ReaderLocal       bool
	ReaderKeyRequired bool
	LocalImportSource string
	Sessions          *auth.Sessions
	AdminSessions     *auth.Sessions
	AccessSessions    *auth.Sessions
	Sync              *syncer.Manager
}

func (w Wiki) handler() handler {
	cfg := Config{Public: w.Public, ReaderLocal: w.ReaderLocal, ReaderKeyRequired: w.ReaderKeyRequired, LocalImportSource: w.LocalImportSource, AllowedHosts: w.AllowedHosts, OwnerToken: w.Token, ReaderToken: w.ReaderToken}
	deps := Deps{Sessions: w.Sessions, AdminSessions: w.AdminSessions, AccessSessions: w.AccessSessions, Sync: w.Sync}
	if w.Store != nil {
		deps.Store, deps.Observations = w.Store.Store, w.Store.observations
	}
	return New(cfg, deps).(handler)
}

func (w Wiki) ServeHTTP(rw http.ResponseWriter, r *http.Request) { w.handler().ServeHTTP(rw, r) }

func openStore(dir string) (*Store, error) {
	st, err := store.Open(dir)
	if err != nil {
		return nil, err
	}
	return &Store{Store: st, observations: &importer.Observations{}}, nil
}

func testStore(t *testing.T) *Store {
	t.Helper()
	s, e := openStore(filepath.Join(t.TempDir(), "data"))
	if e != nil {
		t.Fatal(e)
	}
	return s
}

// state returns a copy of the committed state (storetest.Read).
func (s *Store) state(t testing.TB) domain.Database { return storetest.Read(t, s.Store) }

// mutate commits fn's change to the whole state (storetest.Mutate).
func (s *Store) mutate(t testing.TB, fn func(db *domain.Database)) { storetest.Mutate(t, s.Store, fn) }

// observedAt reads a source's last import observation time.
func observedAt(s *Store, sourceID string) time.Time {
	at, _ := s.observations.Observed(sourceID)
	return at
}

func source(t *testing.T, s *Store, id, visibility string) {
	t.Helper()
	if e := s.addSource(domain.Source{ID: id, Title: id, Kind: "wiki", Visibility: visibility}); e != nil {
		t.Fatal(e)
	}
}

func request(w Wiki, method, url, body string, auth bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, url, strings.NewReader(body))
	if auth {
		r.Header.Set("Authorization", "Bearer test-owner")
	}
	rw := httptest.NewRecorder()
	w.ServeHTTP(rw, r)
	return rw
}

// scopedView returns the access key's snapshot as the handler would serve it.
func scopedView(s *Store, token string, now time.Time) (*Store, bool, error) {
	view, valid, err := s.auth().ScopedView(token, now)
	if view == nil {
		return nil, valid, err
	}
	return s.scoped(view), valid, err
}

// Area operations the tests call directly to prepare state.

func newSyncManager(s *Store, g *github.Client) *syncer.Manager {
	imp := s.importer()
	return syncer.New(s.Store, g, &imp)
}

func (s *Store) connections() []domain.GitHubConnection { return s.syncer().Connections() }
func (s *Store) jobs() []domain.SyncJob                 { return s.syncer().Jobs() }
func (s *Store) mirrors() []domain.SourceMirror         { return s.syncer().Mirrors() }
func (s *Store) retryConnection(id string) error        { return s.syncer().RetryConnection(id) }

func (s *Store) pauseConnection(id string, paused bool) error {
	return s.syncer().PauseConnection(id, paused)
}

func (s *Store) saveAccount(ctx context.Context, g *github.Client, id string, secret github.Secret) (domain.GitHubAccount, error) {
	return newSyncManager(s, g).SaveAccount(ctx, id, secret)
}

func (s *Store) configureMirror(ctx context.Context, in syncer.MirrorRequest) (domain.SourceMirror, error) {
	return s.syncer().ConfigureMirror(ctx, in)
}

func (s *Store) reconcileSources(ctx context.Context, in syncer.ReconcileRequest) (syncer.ReconcileResult, error) {
	return s.syncer().ReconcileSources(ctx, in)
}

func (s *Store) importBatch(b importer.Batch) (importer.Result, error) { return s.importer().Import(b) }

func (s *Store) applyImport(b importer.Batch, proof *importer.RemoteProof, localRoot bool) (importer.Result, error) {
	return s.importer().Apply(b, proof, localRoot)
}

func (s *Store) conflicts() []importer.ConflictRecord { return s.importer().Conflicts() }

func (s *Store) resolveConflict(sourceID, p string, index, expected int, resolution string) error {
	return s.importer().ResolveConflict(sourceID, p, index, expected, resolution)
}

func (s *Store) removeLocalImportedSource(id, expectedGeneration string) (int, error) {
	return s.importer().RemoveLocalImportedSource(id, expectedGeneration)
}

func (s *Store) copyArticle(in transfer.CopyRequest) (domain.Revision, error) {
	return s.transfer().Copy(in)
}

func (s *Store) moveArticle(in transfer.MoveRequest) (domain.Move, error) {
	return s.transfer().Move(in)
}

func (s *Store) moves() []domain.Move { return s.transfer().Moves() }

func (s *Store) verifyAsset(ref domain.BlobRef) error { return s.VerifyBlob(ref) }

func (s *Store) agentContext(sourceID, target string, public bool) (agentctx.Result, error) {
	return agentctx.Context(s.Store, s.observations, sourceID, target, public)
}

// transferPlan decodes a transfer plan response with its source statuses.
type transferPlan struct {
	Articles          []transfer.PlanItem `json:"articles"`
	SourceStatus      domain.SourceStatus `json:"source_status"`
	DestinationStatus domain.SourceStatus `json:"destination_status"`
}

func programSource(t *testing.T, s *Store, id, visibility string) {
	t.Helper()
	if err := s.addSource(domain.Source{ID: id, Title: id, Kind: "program", Visibility: visibility}); err != nil {
		t.Fatal(err)
	}
}
