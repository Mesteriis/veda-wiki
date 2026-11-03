package importer

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store"
	"github.com/Mesteriis/veda-wiki/internal/store/storetest"
)

func gitFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, text := range files {
		writeFixture(t, dir, name, text)
	}
	gitTest(t, dir, "init", "-q")
	gitTest(t, dir, "add", ".")
	gitTest(t, dir, "commit", "-qm", "Fixture")
	return dir
}

func gitTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	argv := append([]string{"-C", dir, "-c", "core.hooksPath=/dev/null", "-c", "init.templateDir=", "-c", "commit.gpgsign=false", "-c", "user.name=Test", "-c", "user.email=test@example.invalid"}, args...)
	if out, e := exec.Command("git", argv...).CombinedOutput(); e != nil {
		t.Fatalf("Git fixture: %v %s", e, out)
	}
}

func writeFixture(t *testing.T, dir, name, text string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte(text), 0600); e != nil {
		t.Fatal(e)
	}
}

const metadataFixture = "schema_version: 1\nkind: wiki\ntitle: Handbook\ndescription: Team documentation\n"

func testBatch() Batch {
	empty := ""
	return Batch{SourceID: "handbook", Kind: "wiki", Metadata: &domain.WikiMetadata{SchemaVersion: 1, Kind: "wiki", Title: "Handbook"}, Files: map[string]string{"README.md": "# Initial"}, Commit: strings.Repeat("a", 40), Complete: true, Create: true, Visibility: "private", ExpectedImport: &empty}
}
func applyAgain(s *fixture, b Batch) Batch {
	b.Create = false
	g := s.db().Sources[b.SourceID].ImportGeneration
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

// fixture is an import service over a fresh store, with the reads the
// import tests check.
type fixture struct {
	Service
	t testing.TB
}

func newFixture(t testing.TB) *fixture {
	t.Helper()
	return &fixture{Service: Service{Store: storetest.New(t), Observations: &Observations{}}, t: t}
}

// reopen opens dir as a restarted process would: no observations.
func (s *fixture) reopen(dir string) (*fixture, error) {
	st, err := store.Open(dir)
	if err != nil {
		return nil, err
	}
	return &fixture{Service: Service{Store: st, Observations: &Observations{}}, t: s.t}, nil
}

func (s *fixture) importBatch(b Batch) (Result, error) { return s.Import(b) }

func (s *fixture) db() domain.Database { return storetest.Read(s.t, s.Store) }

func (s *fixture) observedAt(sourceID string) time.Time {
	at, _ := s.Observations.Observed(sourceID)
	return at
}

// put appends a local revision to an article of a disconnected private
// source, with the revision and conflict checks of a local edit.
func (s *fixture) put(sourceID, p, text string, expected int) (domain.Revision, error) {
	var rev domain.Revision
	err := s.Store.Update(func(tx *store.Tx) error {
		db := tx.DB()
		src, ok := db.Sources[sourceID]
		if !ok {
			return domain.ErrNotFound
		}
		if src.Visibility != "private" || src.ConnectionID != "" {
			s.t.Fatal("fixture put supports only disconnected private sources")
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
		rev = domain.Revision{Number: expected + 1, Text: text, SHA: domain.Digest([]byte(text)), Created: time.Now().UTC().Format(time.RFC3339Nano)}
		article.SourceID, article.Path, article.Deleted, article.PublishedLocal = sourceID, p, false, false
		article.Versions = append(append([]domain.Revision(nil), article.Versions...), rev)
		db.Articles[key] = article
		domain.ReconcileMoves(db)
		return nil
	})
	return rev, err
}

// page returns the owner's current revision of a live article.
func (s *fixture) page(key string) (domain.Revision, error) {
	a, ok := s.db().Articles[key]
	if !ok || a.Deleted {
		return domain.Revision{}, domain.ErrNotFound
	}
	return a.Versions[len(a.Versions)-1], nil
}

func (s *fixture) article(key string) (domain.Article, error) {
	a, ok := s.db().Articles[key]
	if !ok || a.Deleted {
		return domain.Article{}, domain.ErrNotFound
	}
	return a, nil
}

// livePages counts the articles that are not deleted.
func (s *fixture) livePages() []string {
	var out []string
	for key, a := range s.db().Articles {
		if !a.Deleted {
			out = append(out, key)
		}
	}
	return out
}
