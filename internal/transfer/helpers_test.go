package transfer

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Mesteriis/veda-wiki/internal/auth"
	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store"
	"github.com/Mesteriis/veda-wiki/internal/store/storetest"
	"github.com/Mesteriis/veda-wiki/internal/translation"
)

// fixture is a transfer service over a fresh store, with the reads and
// local edits the transfer tests use.
type fixture struct {
	Service
	t testing.TB
}

func newFixture(t testing.TB) *fixture {
	t.Helper()
	return &fixture{Service: Service{Store: storetest.New(t)}, t: t}
}

// reopen opens the store's directory as a restarted process would.
func (s *fixture) reopen() (*fixture, error) {
	st, err := store.Open(s.Store.Dir())
	if err != nil {
		return nil, err
	}
	return &fixture{Service: Service{Store: st}, t: s.t}, nil
}

func (s *fixture) source(id, visibility string) {
	s.t.Helper()
	storetest.Source(s.t, s.Store, id, visibility)
}

func (s *fixture) db() domain.Database { return storetest.Read(s.t, s.Store) }

// state is the committed state as JSON, to prove a failure changed nothing.
func (s *fixture) state() string {
	s.t.Helper()
	b, err := json.Marshal(s.db())
	if err != nil {
		s.t.Fatal(err)
	}
	return string(b)
}

func (s *fixture) mutate(fn func(db *domain.Database)) {
	s.t.Helper()
	storetest.Mutate(s.t, s.Store, fn)
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
		if src.ConnectionID != "" {
			s.t.Fatal("fixture put supports only disconnected sources")
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

// visible reports whether an article is live for the owner or, with public,
// for anonymous readers.
func (s *fixture) visible(key string, public bool) error {
	db := s.db()
	a, ok := db.Articles[key]
	if !ok || a.Deleted {
		return domain.ErrNotFound
	}
	src := db.Sources[a.SourceID]
	if public && (!domain.SourcePublicReadable(db, src, time.Now()) || (src.ConnectionID != "" && a.Published == nil)) {
		return domain.ErrNotFound
	}
	return nil
}

func smallPNG(t testing.TB) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := png.Encode(&out, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// asset stores a small image blob and makes it the source's only asset.
func (s *fixture) asset(sourceID, assetPath string) domain.BlobRef {
	s.t.Helper()
	image := smallPNG(s.t)
	ref, err := domain.ImageRef(image)
	if err != nil {
		s.t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(s.Store.Dir(), "blobs"), 0700); err != nil {
		s.t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.Store.Dir(), "blobs", ref.SHA), image, 0600); err != nil {
		s.t.Fatal(err)
	}
	s.mutate(func(db *domain.Database) {
		src := db.Sources[sourceID]
		src.Assets = map[string]domain.BlobRef{assetPath: ref}
		db.Sources[sourceID] = src
	})
	return ref
}

func (s *fixture) programSource(id, visibility string) {
	s.t.Helper()
	s.mutate(func(db *domain.Database) {
		db.Sources[id] = domain.Source{ID: id, Title: id, Kind: "program", Visibility: visibility}
	})
}

// page is the reader API's view of an article, with its translation state.
func (s *fixture) page(key string, public bool) (domain.Page, error) {
	return domain.ReadPage(s.db(), key, public, time.Now())
}

// article is the article API's view: the owner's revisions or, with public,
// the published revision only.
func (s *fixture) article(sourceID, p string, public bool) (domain.Article, error) {
	return domain.ReadArticle(s.db(), sourceID, p, public, time.Now())
}

func (s *fixture) auth() auth.Service { return auth.Service{Store: s.Store} }

func (s *fixture) translation() translation.Service {
	return translation.Service{Store: s.Store}
}
