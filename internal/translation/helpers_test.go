package translation

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Mesteriis/veda-wiki/internal/domain"
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

// article is the owner's view of a live article.
func (s *fixture) article(sourceID, p string, _ bool) (domain.Article, error) {
	var a domain.Article
	err := s.Store.View(func(db *domain.Database) error {
		var ok bool
		a, ok = db.Articles[sourceID+"/"+p]
		if !ok || a.Deleted {
			return domain.ErrNotFound
		}
		a.Versions = append([]domain.Revision(nil), a.Versions...)
		return nil
	})
	return a, err
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
		if public && domain.CredentialPattern.MatchString(text) {
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
