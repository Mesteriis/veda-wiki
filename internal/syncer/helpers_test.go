package syncer

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Mesteriis/veda-wiki/internal/auth"
	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/github"
	"github.com/Mesteriis/veda-wiki/internal/importer"
	"github.com/Mesteriis/veda-wiki/internal/store"
	"github.com/Mesteriis/veda-wiki/internal/store/storetest"
	"github.com/Mesteriis/veda-wiki/internal/transfer"
	"github.com/Mesteriis/veda-wiki/internal/translation"
)

const metadataFixture = "schema_version: 1\nkind: wiki\ntitle: Handbook\ndescription: Team documentation\n"

// fixtureStore is a store with its one set of import observations and the
// reads, local edits and area operations the synchronization tests use. The
// local edit and the reader views reproduce the rules of the owner API.
type fixtureStore struct {
	st           *store.Store
	t            testing.TB
	observations *importer.Observations
}

func testStore(t testing.TB) *fixtureStore {
	t.Helper()
	return &fixtureStore{st: storetest.New(t), t: t, observations: &importer.Observations{}}
}

// openStore opens an existing data directory as a new process would.
func openStore(t testing.TB, dir string) (*fixtureStore, error) {
	st, err := store.Open(dir)
	if err != nil {
		return nil, err
	}
	return &fixtureStore{st: st, t: t, observations: &importer.Observations{}}, nil
}

// reopen opens the store's directory as a restarted process would.
func (s *fixtureStore) reopen() (*fixtureStore, error) { return openStore(s.t, s.Dir()) }

func (s *fixtureStore) Dir() string { return s.st.Dir() }

func (s *fixtureStore) importer() *importer.Service {
	return &importer.Service{Store: s.st, Observations: s.observations}
}

func (s *fixtureStore) transfer() transfer.Service { return transfer.Service{Store: s.st} }

func (s *fixtureStore) translation() translation.Service { return translation.Service{Store: s.st} }

// newSyncManager returns the manager the process would run over s.
func newSyncManager(s *fixtureStore, g *github.Client) *Manager { return New(s.st, g, s.importer()) }

func (s *fixtureStore) manager() *Manager { return newSyncManager(s, nil) }

// db is the committed state.
func (s *fixtureStore) db() domain.Database { return storetest.Read(s.t, s.st) }

// mutate changes the committed state directly, as a test fixture.
func (s *fixtureStore) mutate(fn func(db *domain.Database)) {
	s.t.Helper()
	storetest.Mutate(s.t, s.st, fn)
}

// holdLock holds the store write lock, as a concurrent writer would, until
// the returned release is called.
func (s *fixtureStore) holdLock() (release func()) {
	held, done := make(chan struct{}), make(chan struct{})
	unblock := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.st.Exclusive(func(*store.Exclusive) error {
			close(held)
			<-unblock
			return nil
		})
	}()
	<-held
	return func() {
		close(unblock)
		<-done
	}
}

func (s *fixtureStore) observedAt(sourceID string) time.Time {
	at, _ := s.observations.Observed(sourceID)
	return at
}

func (s *fixtureStore) connections() []domain.GitHubConnection { return s.manager().Connections() }
func (s *fixtureStore) jobs() []domain.SyncJob                 { return s.manager().Jobs() }
func (s *fixtureStore) accounts() []domain.GitHubAccount       { return s.manager().Accounts() }
func (s *fixtureStore) mirrors() []domain.SourceMirror         { return s.manager().Mirrors() }
func (s *fixtureStore) retryConnection(id string) error        { return s.manager().RetryConnection(id) }

func (s *fixtureStore) pauseConnection(id string, paused bool) error {
	return s.manager().PauseConnection(id, paused)
}

func (s *fixtureStore) publishJob(id string, expected int, confirm bool) error {
	return s.manager().PublishJob(id, expected, confirm)
}

func (s *fixtureStore) cancelBundle(id string, confirm bool) error {
	return s.manager().CancelBundle(id, confirm)
}

func (s *fixtureStore) saveAccount(ctx context.Context, g *github.Client, id string, secret github.Secret) (domain.GitHubAccount, error) {
	return newSyncManager(s, g).SaveAccount(ctx, id, secret)
}

func (s *fixtureStore) configureMirror(ctx context.Context, in MirrorRequest) (domain.SourceMirror, error) {
	return s.manager().ConfigureMirror(ctx, in)
}

func (s *fixtureStore) reconcileSources(ctx context.Context, in ReconcileRequest) (ReconcileResult, error) {
	return s.manager().ReconcileSources(ctx, in)
}

func (s *fixtureStore) reconcileSourcesExpected(ctx context.Context, in ReconcileRequest, revision *int) (ReconcileResult, error) {
	return s.manager().reconcile(ctx, in, revision)
}

func (s *fixtureStore) connectionStatusFor(observed domain.GitHubConnection, state, verification, code string, info *github.RepositoryInfo, retryAt time.Time) error {
	return s.manager().connectionStatusFor(observed, state, verification, code, info, retryAt)
}

func (s *fixtureStore) setJobFor(expected domain.GitHubConnection, original domain.SyncJob, state, code, prepared string) error {
	return s.manager().setJobFor(expected, original, state, code, prepared)
}

func (s *fixtureStore) secret(accountID string) (*github.Secret, domain.GitHubAccount, error) {
	return s.manager().secret(accountID)
}

func (s *fixtureStore) adoptionSnapshot(id string) (string, error) {
	return s.manager().adoptionSnapshot(id)
}

func (s *fixtureStore) importBatch(b importer.Batch) (importer.Result, error) {
	return s.importer().Import(b)
}

func (s *fixtureStore) applyImport(b importer.Batch, proof *importer.RemoteProof, localRoot bool) (importer.Result, error) {
	return s.importer().Apply(b, proof, localRoot)
}

func (s *fixtureStore) conflicts() []importer.ConflictRecord { return s.importer().Conflicts() }

func (s *fixtureStore) resolveConflict(sourceID, p string, index, expected int, resolution string) error {
	return s.importer().ResolveConflict(sourceID, p, index, expected, resolution)
}

func (s *fixtureStore) copyArticle(in transfer.CopyRequest) (domain.Revision, error) {
	return s.transfer().Copy(in)
}

func (s *fixtureStore) moveArticle(in transfer.MoveRequest) (domain.Move, error) {
	return s.transfer().Move(in)
}

func (s *fixtureStore) moves() []domain.Move { return s.transfer().Moves() }

func (s *fixtureStore) cancelMove(id string, confirm bool) error {
	return s.transfer().CancelMove(id, confirm)
}

func (s *fixtureStore) verifyAsset(ref domain.BlobRef) error { return s.st.VerifyBlob(ref) }

// addSource adds a disconnected source, as the owner API does.
func (s *fixtureStore) addSource(src domain.Source) error {
	return s.st.Update(func(tx *store.Tx) error {
		if _, ok := tx.DB().Sources[src.ID]; ok {
			return domain.ErrConflict
		}
		tx.DB().Sources[src.ID] = src
		return nil
	})
}

func source(t testing.TB, s *fixtureStore, id, visibility string) {
	t.Helper()
	if err := s.addSource(domain.Source{ID: id, Title: id, Kind: "wiki", Visibility: visibility}); err != nil {
		t.Fatal(err)
	}
}

// put saves a local revision with the checks, publication and outbox entry
// of an owner edit.
func (s *fixtureStore) put(sourceID, p, text string, expected int) (domain.Revision, error) {
	if len(text) > domain.MaxArticleBytes || !utf8.ValidString(text) || expected < 0 {
		return domain.Revision{}, domain.ErrInvalid
	}
	var rev domain.Revision
	err := s.st.Update(func(tx *store.Tx) error {
		db := tx.DB()
		src, ok := db.Sources[sourceID]
		if !ok {
			return domain.ErrNotFound
		}
		if !domain.ArticlePath(p, src.Kind) {
			return domain.ErrInvalid
		}
		key := sourceID + "/" + p
		if domain.MoveDeletionLocks(*db, sourceID, p) {
			return domain.ErrConflict
		}
		article := db.Articles[key]
		if len(article.Versions) != expected {
			return domain.ErrConflict
		}
		for _, conflict := range article.Conflicts {
			if conflict.Resolution == "" {
				return domain.ErrConflict
			}
		}
		publicNow := domain.SourcePublic(*db, src, time.Now())
		if publicNow && domain.CredentialPattern.MatchString(text) {
			return domain.ErrInvalid
		}
		rev = domain.Revision{Number: expected + 1, Text: text, SHA: domain.Digest([]byte(text)), Created: time.Now().UTC().Format(time.RFC3339Nano)}
		article.SourceID, article.Path, article.Deleted = sourceID, p, false
		article.Versions = append(append([]domain.Revision(nil), article.Versions...), rev)
		if publicNow {
			published := rev
			published.Number = 0
			article.Published = &published
			article.PublishedLocal = true
		} else {
			article.PublishedLocal = false
		}
		// Reconciliation can delete another article when a pending move
		// completes. Such writes must persist the whole state.
		activeMove := false
		for _, move := range db.Moves {
			switch move.State {
			case "complete", "cancelled", "source_changed", "destination_changed":
				continue
			default:
				activeMove = true
			}
			break
		}
		db.Articles[key] = article
		domain.ReconcileMoves(db)
		if src.ConnectionID != "" {
			domain.EnqueueWrite(db, article, rev, time.Now())
		}
		if !activeMove {
			tx.OnlyArticle(key)
		}
		return nil
	})
	if err != nil {
		return domain.Revision{}, err
	}
	return rev, nil
}

// readable returns the revision a reader sees, as the reader API selects it.
func readable(db domain.Database, key string, public bool) (domain.Article, domain.Source, domain.Revision, error) {
	a, ok := db.Articles[key]
	if !ok || a.Deleted {
		return a, domain.Source{}, domain.Revision{}, domain.ErrNotFound
	}
	src := db.Sources[a.SourceID]
	if public && !domain.SourcePublicReadable(db, src, time.Now()) {
		return a, src, domain.Revision{}, domain.ErrNotFound
	}
	r := a.Versions[len(a.Versions)-1]
	if public && src.ConnectionID != "" {
		if a.Published == nil {
			return a, src, domain.Revision{}, domain.ErrNotFound
		}
		r = *a.Published
	}
	return a, src, r, nil
}

// page is the article view of the reader API, without its translation state.
func (s *fixtureStore) page(key string, public bool) (domain.Page, error) {
	a, src, r, err := readable(s.db(), key, public)
	if err != nil {
		return domain.Page{}, err
	}
	originalSHA := r.SHA
	r, translated := domain.DisplayedRevision(a, r, public)
	return domain.Page{Path: key, Title: domain.Title(r.Text, a.Path), Environment: src.ID, SHA: r.SHA, Updated: r.Created, Text: r.Text, OriginalSHA: originalSHA, Translated: translated}, nil
}

// article is the stored article as the owner reads it, or with public its
// single published revision.
func (s *fixtureStore) article(sourceID, p string, public bool) (domain.Article, error) {
	a, _, r, err := readable(s.db(), sourceID+"/"+p, public)
	if err != nil {
		return domain.Article{}, err
	}
	if public {
		r.Number = 0
		return domain.Article{SourceID: a.SourceID, Path: a.Path, Versions: []domain.Revision{r}}, nil
	}
	a.Versions = append([]domain.Revision(nil), a.Versions...)
	return a, nil
}

type snapshotView struct{ Pages []domain.Page }

// snapshot lists the readable pages by key.
func (s *fixtureStore) snapshot(public bool) snapshotView {
	db := s.db()
	view := snapshotView{Pages: []domain.Page{}}
	for key := range db.Articles {
		if p, err := s.page(key, public); err == nil {
			view.Pages = append(view.Pages, p)
		}
	}
	sort.Slice(view.Pages, func(i, j int) bool { return view.Pages[i].Path < view.Pages[j].Path })
	return view
}

func testBatch() importer.Batch {
	empty := ""
	return importer.Batch{SourceID: "handbook", Kind: "wiki", Metadata: &domain.WikiMetadata{SchemaVersion: 1, Kind: "wiki", Title: "Handbook"}, Files: map[string]string{"README.md": "# Initial"}, Commit: strings.Repeat("a", 40), Complete: true, Create: true, Visibility: "private", ExpectedImport: &empty}
}

func smallPNG(t testing.TB) []byte {
	t.Helper()
	var out bytes.Buffer
	if e := png.Encode(&out, image.NewRGBA(image.Rect(0, 0, 2, 2))); e != nil {
		t.Fatal(e)
	}
	return out.Bytes()
}

// fixtureAsset stores a small image blob and makes it the source's only asset.
func fixtureAsset(t testing.TB, s *fixtureStore, sourceID, assetPath string) domain.BlobRef {
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
	s.mutate(func(db *domain.Database) {
		src := db.Sources[sourceID]
		src.Assets = map[string]domain.BlobRef{assetPath: ref}
		db.Sources[sourceID] = src
	})
	return ref
}

func errorsIsConflictOrInvalid(e error) bool {
	return e == domain.ErrInvalid || e == domain.ErrConflict
}

// jsonResponse writes a JSON response as the fake GitHub servers do.
func jsonResponse(rw http.ResponseWriter, status int, v any) {
	rw.Header().Set("Content-Type", "application/json")
	rw.Header().Set("Cache-Control", "no-store")
	rw.Header().Set("X-Content-Type-Options", "nosniff")
	rw.WriteHeader(status)
	json.NewEncoder(rw).Encode(v)
}

func (s *fixtureStore) auth() auth.Service { return auth.Service{Store: s.st} }

func programSource(t testing.TB, s *fixtureStore, id, visibility string) {
	t.Helper()
	if err := s.addSource(domain.Source{ID: id, Title: id, Kind: "program", Visibility: visibility}); err != nil {
		t.Fatal(err)
	}
}
