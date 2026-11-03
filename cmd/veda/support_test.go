package main

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/agentctx"
	"github.com/Mesteriis/veda-wiki/internal/auth"
	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/github"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
	"github.com/Mesteriis/veda-wiki/internal/httpapi"
	"github.com/Mesteriis/veda-wiki/internal/importer"
	"github.com/Mesteriis/veda-wiki/internal/store"
	"github.com/Mesteriis/veda-wiki/internal/syncer"
	"github.com/Mesteriis/veda-wiki/internal/transfer"
	"github.com/Mesteriis/veda-wiki/internal/translation"
)

// Test support for the startup, CLI and integration tests that stay in
// cmd/veda until Task 13 moves them. Store pairs the shared store with the
// process's import observations, as the HTTP layer does, and offers the area
// operations these tests prepare state with.
type Store struct {
	*store.Store
	observations *importer.Observations
}

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

func (s *Store) scoped(view *store.Store) *Store {
	var granted []string
	_ = view.View(func(db *domain.Database) error {
		for id := range db.Sources {
			granted = append(granted, id)
		}
		return nil
	})
	return &Store{Store: view, observations: s.observations.Scoped(granted)}
}

func (s *Store) auth() auth.Service { return auth.Service{Store: s.Store} }

func (s *Store) importer() importer.Service {
	return importer.Service{Store: s.Store, Observations: s.observations}
}

func (s *Store) sourceStatus(db domain.Database, sourceID string, src domain.Source) domain.SourceStatus {
	return agentctx.Status(db, s.observations, sourceID, src, time.Now())
}

func (s *Store) transfer() transfer.Service {
	return transfer.Service{Store: s.Store, Status: s.sourceStatus}
}

func (s *Store) translation() translation.Service {
	return translation.Service{Store: s.Store, Status: s.sourceStatus}
}

func (s *Store) syncer(g *github.Client) *syncer.Manager {
	imp := s.importer()
	return syncer.New(s.Store, g, &imp)
}

func (s *Store) addSource(src domain.Source) error {
	return s.Update(func(tx *store.Tx) error { return domain.AddSource(tx.DB(), src) })
}

func (s *Store) put(sourceID, p, text string, expected int) (domain.Revision, error) {
	var rev domain.Revision
	err := s.Update(func(tx *store.Tx) error {
		written, key, err := domain.PutArticle(tx.DB(), sourceID, p, text, expected, time.Now())
		if key != "" {
			tx.OnlyArticle(key)
		}
		rev = written
		return err
	})
	return rev, err
}

func (s *Store) snapshot(public bool) domain.Snapshot {
	var snap domain.Snapshot
	_ = s.View(func(db *domain.Database) error {
		snap = domain.ReadSnapshot(*db, public, time.Now())
		return nil
	})
	return snap
}

func (s *Store) page(key string, public bool) (domain.Page, error) {
	var page domain.Page
	err := s.View(func(db *domain.Database) (err error) {
		page, err = domain.ReadPage(*db, key, public, time.Now())
		return err
	})
	return page, err
}

func (s *Store) article(sourceID, p string, public bool) (domain.Article, error) {
	var a domain.Article
	err := s.View(func(db *domain.Database) (err error) {
		a, err = domain.ReadArticle(*db, sourceID, p, public, time.Now())
		return err
	})
	return a, err
}

func (s *Store) proposalBase(sourceID, path string) (map[string]any, error) {
	return agentctx.ProposalBase(s.Store, s.observations, sourceID, path)
}

func newSyncManager(s *Store, g *github.Client) *syncer.Manager { return s.syncer(g) }

func (s *Store) jobs() []domain.SyncJob { return s.syncer(nil).Jobs() }

func (s *Store) retryConnection(id string) error { return s.syncer(nil).RetryConnection(id) }

func (s *Store) pauseConnection(id string, paused bool) error {
	return s.syncer(nil).PauseConnection(id, paused)
}

func (s *Store) conflicts() []importer.ConflictRecord { return s.importer().Conflicts() }

func (s *Store) resolveConflict(sourceID, p string, index, expected int, resolution string) error {
	return s.importer().ResolveConflict(sourceID, p, index, expected, resolution)
}

func (s *Store) planTransfer(in transfer.CopyRequest) (transfer.Plan, error) {
	return s.transfer().PlanTransfer(in)
}

func (s *Store) saveAccount(ctx context.Context, g *github.Client, id string, secret github.Secret) (domain.GitHubAccount, error) {
	return s.syncer(g).SaveAccount(ctx, id, secret)
}

func (s *Store) importBatch(b importer.Batch) (importer.Result, error) { return s.importer().Import(b) }

func (s *Store) copyArticle(in transfer.CopyRequest) (domain.Revision, error) {
	return s.transfer().Copy(in)
}

func (s *Store) moveArticle(in transfer.MoveRequest) (domain.Move, error) {
	return s.transfer().Move(in)
}

func (s *Store) moves() []domain.Move { return s.transfer().Moves() }

// Wiki describes one listener the way the tests configure it; ServeHTTP
// serves each request through httpapi.New.
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

func (w Wiki) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	cfg := httpapi.Config{Public: w.Public, ReaderLocal: w.ReaderLocal, ReaderKeyRequired: w.ReaderKeyRequired, LocalImportSource: w.LocalImportSource, AllowedHosts: w.AllowedHosts, OwnerToken: w.Token, ReaderToken: w.ReaderToken}
	deps := httpapi.Deps{Sessions: w.Sessions, AdminSessions: w.AdminSessions, AccessSessions: w.AccessSessions, Sync: w.Sync}
	if w.Store != nil {
		deps.Store, deps.Observations = w.Store.Store, w.Store.observations
	}
	httpapi.New(cfg, deps).ServeHTTP(rw, r)
}

func source(t *testing.T, s *Store, id, visibility string) {
	t.Helper()
	if e := s.addSource(domain.Source{ID: id, Title: id, Kind: "wiki", Visibility: visibility}); e != nil {
		t.Fatal(e)
	}
}

func scopedView(s *Store, token string, now time.Time) (*Store, bool, error) {
	view, valid, err := s.auth().ScopedView(token, now)
	if view == nil {
		return nil, valid, err
	}
	return s.scoped(view), valid, err
}

func connectFixture(t *testing.T, s *Store, f *githubtest.Fake, write bool) *syncer.Manager {
	t.Helper()
	m := newSyncManager(s, f.Client())
	account := ""
	if write || f.Private {
		if _, e := s.saveAccount(context.Background(), f.Client(), "operator", github.Secret{Kind: "token", Token: githubtest.Access}); e != nil {
			t.Fatal(e)
		}
		account = "operator"
	}
	if _, e := m.Connect(context.Background(), syncer.ConnectRequest{ID: "handbook", Repository: "https://github.com/org/handbook", AccountID: account, Write: write}); e != nil {
		t.Fatal(e)
	}
	m.Once(context.Background())
	return m
}

func syncFixtureSource(t *testing.T, m *syncer.Manager, id string) {
	t.Helper()
	for _, conn := range m.Connections() {
		if conn.ID == id {
			m.SyncSource(context.Background(), conn)
			return
		}
	}
	t.Fatalf("missing connection %s", id)
}

const metadataFixture = "schema_version: 1\nkind: wiki\ntitle: Handbook\ndescription: Team documentation\n"

func testBatch() importer.Batch {
	empty := ""
	return importer.Batch{SourceID: "handbook", Kind: "wiki", Metadata: &domain.WikiMetadata{SchemaVersion: 1, Kind: "wiki", Title: "Handbook"}, Files: map[string]string{"README.md": "# Initial"}, Commit: strings.Repeat("a", 40), Complete: true, Create: true, Visibility: "private", ExpectedImport: &empty}
}

func applyAgain(s *Store, b importer.Batch) importer.Batch {
	b.Create = false
	g := s.DB().Sources[b.SourceID].ImportGeneration
	b.ExpectedImport = &g
	return b
}

func smallPNG(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	if e := png.Encode(&out, image.NewRGBA(image.Rect(0, 0, 2, 2))); e != nil {
		t.Fatal(e)
	}
	return out.Bytes()
}

func fixtureAsset(t *testing.T, s *Store, sourceID, assetPath string) domain.BlobRef {
	t.Helper()
	image := smallPNG(t)
	ref, err := domain.ImageRef(image)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(s.Dir(), "blobs"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.Dir(), "blobs", ref.SHA), image, 0600); err != nil {
		t.Fatal(err)
	}
	if err = s.Update(func(tx *store.Tx) error {
		src := tx.DB().Sources[sourceID]
		src.Assets = map[string]domain.BlobRef{assetPath: ref}
		tx.DB().Sources[sourceID] = src
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return ref
}

// jsonResponse answers a fake translation service.
func jsonResponse(rw http.ResponseWriter, status int, v any) {
	rw.Header().Set("Content-Type", "application/json; charset=utf-8")
	rw.WriteHeader(status)
	_ = json.NewEncoder(rw).Encode(v)
}
