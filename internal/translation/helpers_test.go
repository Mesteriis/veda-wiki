package translation

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/importer"
	"github.com/Mesteriis/veda-wiki/internal/store"
	"github.com/Mesteriis/veda-wiki/internal/store/storetest"
)

// fixture is a translation service over a fresh store, with the local
// edits and reads the translation tests use.
type fixture struct {
	Service
	t testing.TB
}

func newFixture(t testing.TB) *fixture {
	t.Helper()
	return &fixture{Service: Service{Store: storetest.New(t)}, t: t}
}

// reopenFixture opens a data directory as a restarted process would.
func reopenFixture(dir string) (*fixture, error) {
	st, err := store.Open(dir)
	if err != nil {
		return nil, err
	}
	return &fixture{Service: Service{Store: st}}, nil
}

func (s *fixture) Dir() string { return s.Store.Dir() }

func (s *fixture) addSource(src domain.Source) error {
	return s.Store.Update(func(tx *store.Tx) error {
		tx.DB().Sources[src.ID] = src
		return nil
	})
}

func programSource(t *testing.T, s *fixture, id, visibility string) {
	t.Helper()
	if err := s.addSource(domain.Source{ID: id, Title: id, Kind: "program", Visibility: visibility}); err != nil {
		t.Fatal(err)
	}
}

func (s *fixture) mutate(fn func(db *domain.Database)) {
	s.t.Helper()
	storetest.Mutate(s.t, s.Store, fn)
}

// article is the article API's view of a live article: the owner's
// revisions or, with public, the published revision only.
func (s *fixture) article(sourceID, p string, public bool) (domain.Article, error) {
	var a domain.Article
	err := s.Store.View(func(db *domain.Database) (err error) {
		a, err = domain.ReadArticle(*db, sourceID, p, public, time.Now())
		return err
	})
	return a, err
}

// page is the reader API's view of an article, with its translation state.
func (s *fixture) page(key string, public bool) (domain.Page, error) {
	var page domain.Page
	err := s.Store.View(func(db *domain.Database) (err error) {
		page, err = domain.ReadPage(*db, key, public, time.Now())
		return err
	})
	return page, err
}

// reopen opens the fixture's data directory as a restarted process would.
func (s *fixture) reopen() (*fixture, error) {
	reopened, err := reopenFixture(s.Dir())
	if reopened != nil {
		reopened.t = s.t
	}
	return reopened, err
}

func (s *fixture) importer() importer.Service {
	return importer.Service{Store: s.Store, Observations: &importer.Observations{}}
}

func (s *fixture) importBatch(b importer.Batch) (importer.Result, error) {
	return s.importer().Import(b)
}

func (s *fixture) resolveConflict(sourceID, p string, index, expected int, resolution string) error {
	return s.importer().ResolveConflict(sourceID, p, index, expected, resolution)
}

func testBatch() importer.Batch {
	empty := ""
	return importer.Batch{SourceID: "handbook", Kind: "wiki", Metadata: &domain.WikiMetadata{SchemaVersion: 1, Kind: "wiki", Title: "Handbook"}, Files: map[string]string{"README.md": "# Initial"}, Commit: strings.Repeat("a", 40), Complete: true, Create: true, Visibility: "private", ExpectedImport: &empty}
}

// applyAgain turns a batch into the next import of the same source.
func applyAgain(s *fixture, b importer.Batch) importer.Batch {
	b.Create = false
	g := storetest.Read(s.t, s.Store).Sources[b.SourceID].ImportGeneration
	b.ExpectedImport = &g
	return b
}

// put appends a local revision to an article of a disconnected source with
// the checks and publication of a local edit.
func (s *fixture) put(sourceID, p, text string, expected int) (domain.Revision, error) {
	if len(text) > domain.MaxArticleBytes || !utf8.ValidString(text) || expected < 0 {
		return domain.Revision{}, domain.ErrInvalid
	}
	var rev domain.Revision
	err := s.Store.Update(func(tx *store.Tx) error {
		db := tx.DB()
		src, ok := db.Sources[sourceID]
		if !ok {
			return domain.ErrNotFound
		}
		if !domain.ArticlePath(p, src.Kind) {
			return domain.ErrInvalid
		}
		if domain.MoveDeletionLocks(*db, sourceID, p) {
			return domain.ErrConflict
		}
		key := sourceID + "/" + p
		article := db.Articles[key]
		if len(article.Versions) != expected {
			return domain.ErrConflict
		}
		for _, conflict := range article.Conflicts {
			if conflict.Resolution == "" {
				return domain.ErrConflict
			}
		}
		now := time.Now()
		public := domain.SourcePublic(*db, src, now)
		if public && domain.CredentialLike(text) {
			return domain.ErrInvalid
		}
		rev = domain.Revision{Number: expected + 1, Text: text, SHA: domain.Digest([]byte(text)), Created: now.UTC().Format(time.RFC3339Nano)}
		article.SourceID, article.Path, article.Deleted = sourceID, p, false
		article.Versions = append(append([]domain.Revision(nil), article.Versions...), rev)
		article.PublishedLocal = public
		if public {
			published := rev
			published.Number = 0
			article.Published = &published
		}
		db.Articles[key] = article
		domain.ReconcileMoves(db)
		return nil
	})
	return rev, err
}

func jsonResponse(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
