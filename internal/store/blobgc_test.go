package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// agedBlob stores an image and sets its modification time to now-age.
func agedBlob(t *testing.T, s *Store, shade uint8, now time.Time, age time.Duration) domain.BlobRef {
	t.Helper()
	ref, err := s.WriteBlob(testPNG(t, shade))
	if err != nil {
		t.Fatal(err)
	}
	setAge(t, s.BlobPath(ref.SHA), now, age)
	return ref
}

func setAge(t *testing.T, name string, now time.Time, age time.Duration) {
	t.Helper()
	if err := os.Chtimes(name, now.Add(-age), now.Add(-age)); err != nil {
		t.Fatal(err)
	}
}

func present(name string) bool {
	_, err := os.Lstat(name)
	return err == nil
}

func TestCollectBlobsRemovesOnlyOldUnreferencedFiles(t *testing.T) {
	s := testStore(t)
	addSource(t, s, "local")
	now := time.Now()
	kept, err := s.WriteBlob(testPNG(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	update(t, s, func(db *domain.Database) {
		src := db.Sources["local"]
		src.Assets = map[string]domain.BlobRef{"images/a.png": kept}
		db.Sources["local"] = src
	})
	setAge(t, s.BlobPath(kept.SHA), now, 48*time.Hour)
	old := agedBlob(t, s, 2, now, 25*time.Hour)
	young := agedBlob(t, s, 3, now, 23*time.Hour)
	dir := filepath.Join(s.Dir(), "blobs")
	strays := map[string]bool{}
	for name, remove := range map[string]bool{".blob-old": true, ".blob-new": false, "notes.txt": false, strings.Repeat("A", 64): false} {
		file := filepath.Join(dir, name)
		if err = os.WriteFile(file, []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
		age := 48 * time.Hour
		if name == ".blob-new" {
			age = time.Hour
		}
		setAge(t, file, now, age)
		strays[file] = remove
	}
	subdir := filepath.Join(dir, strings.Repeat("c", 64))
	if err = os.Mkdir(subdir, 0700); err != nil {
		t.Fatal(err)
	}
	setAge(t, subdir, now, 48*time.Hour)
	target := filepath.Join(t.TempDir(), "target")
	if err = os.WriteFile(target, testPNG(t, 4), 0600); err != nil {
		t.Fatal(err)
	}
	setAge(t, target, now, 48*time.Hour)
	link := filepath.Join(dir, domain.Digest(testPNG(t, 4)))
	if err = os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	removed, err := s.CollectBlobs(now)
	if err != nil || removed != 2 {
		t.Fatal("removed", removed, err)
	}
	if present(s.BlobPath(old.SHA)) {
		t.Fatal("old unreferenced blob kept")
	}
	if s.VerifyBlob(kept) != nil || s.VerifyBlob(young) != nil {
		t.Fatal("referenced or young blob removed")
	}
	for file, remove := range strays {
		if present(file) == remove {
			t.Fatal("unexpected outcome for", filepath.Base(file))
		}
	}
	if !present(subdir) || !present(link) || !present(target) {
		t.Fatal("non-regular entry removed")
	}
}

// referenceSetters place ref in each persisted category ReferencedBlobs reads.
var referenceSetters = map[string]func(db *domain.Database, ref domain.BlobRef){
	"source": func(db *domain.Database, ref domain.BlobRef) {
		src := db.Sources["local"]
		src.Assets = map[string]domain.BlobRef{"images/a.png": ref}
		db.Sources["local"] = src
	},
	"revision": func(db *domain.Database, ref domain.BlobRef) {
		a := db.Articles["local/a.md"]
		r := a.Versions[0]
		r.Assets = map[string]domain.BlobRef{"images/a.png": ref}
		a.Versions = []domain.Revision{r}
		db.Articles["local/a.md"] = a
	},
	"published": func(db *domain.Database, ref domain.BlobRef) {
		a := db.Articles["local/a.md"]
		p := a.Versions[0]
		p.Assets = map[string]domain.BlobRef{"images/a.png": ref}
		a.Published = &p
		db.Articles["local/a.md"] = a
	},
	"outbox": func(db *domain.Database, ref domain.BlobRef) {
		r := db.Articles["local/a.md"].Versions[0]
		db.Outbox["job"] = domain.SyncJob{ID: "job", SourceID: "local", Path: "a.md", Revision: 1, SHA: r.SHA, State: "pending", Visibility: "private", Assets: map[string]domain.BlobRef{"images/a.png": ref}}
	},
	"move": func(db *domain.Database, ref domain.BlobRef) {
		r := db.Articles["local/a.md"].Versions[0]
		id := strings.Repeat("e", 32)
		db.Moves[id] = domain.Move{ID: id, SourceID: "local", SourcePath: "a.md", SourceRevision: 1, DestinationID: "other", DestinationPath: "b.md", SHA: r.SHA, State: "waiting_destination", Assets: map[string]domain.BlobRef{"images/a.png": ref}}
	},
}

func TestCollectBlobsKeepsEveryReferenceCategory(t *testing.T) {
	const text = "# A"
	revision := domain.Revision{Number: 1, Text: text, SHA: domain.Digest([]byte(text))}
	for _, backend := range []string{"json", "sqlite"} {
		for name, set := range referenceSetters {
			t.Run(backend+"/"+name, func(t *testing.T) {
				s := testStore(t)
				update(t, s, func(db *domain.Database) {
					for _, id := range []string{"local", "other"} {
						db.Sources[id] = domain.Source{ID: id, Title: id, Kind: "wiki", Visibility: "private"}
					}
					db.Articles["local/a.md"] = domain.Article{SourceID: "local", Path: "a.md", Versions: []domain.Revision{revision}}
					db.Articles["other/b.md"] = domain.Article{SourceID: "other", Path: "b.md", Versions: []domain.Revision{revision}}
				})
				if backend == "sqlite" {
					if err := MigrateSQLite(s.Dir()); err != nil {
						t.Fatal(err)
					}
				}
				var err error
				if s, err = Open(s.Dir()); err != nil {
					t.Fatal(err)
				}
				kept, err := s.WriteBlob(testPNG(t, 1))
				if err != nil {
					t.Fatal(err)
				}
				update(t, s, func(db *domain.Database) { set(db, kept) })
				if s, err = Open(s.Dir()); err != nil {
					t.Fatal(err)
				}
				now := time.Now()
				setAge(t, s.BlobPath(kept.SHA), now, 48*time.Hour)
				unused := agedBlob(t, s, 2, now, 48*time.Hour)
				removed, err := s.CollectBlobs(now)
				if err != nil || removed != 1 || present(s.BlobPath(unused.SHA)) {
					t.Fatal("collection did not run", removed, err)
				}
				if err = s.VerifyBlob(kept); err != nil {
					t.Fatal("referenced blob removed", err)
				}
			})
		}
	}
}

// Review focus 3: an old unreferenced blob needed again by a concurrent edit
// is written with WriteBlobs before that edit commits. A collection between
// the two must not remove it.
func TestCollectBlobsKeepsBlobRewrittenBeforeCommit(t *testing.T) {
	s := testStore(t)
	addSource(t, s, "local")
	now := time.Now()
	reused := agedBlob(t, s, 1, now, 48*time.Hour)
	control := agedBlob(t, s, 2, now, 48*time.Hour)

	refs, err := s.WriteBlobs(map[string][]byte{"images/a.png": testPNG(t, 1)})
	if err != nil || refs["images/a.png"] != reused {
		t.Fatal(refs, err)
	}
	info, err := os.Stat(s.BlobPath(reused.SHA))
	if err != nil || now.Sub(info.ModTime()) > time.Hour {
		t.Fatal("existing blob not refreshed", err)
	}
	removed, err := s.CollectBlobs(time.Now())
	if err != nil || removed != 1 || present(s.BlobPath(control.SHA)) {
		t.Fatal("collection did not run", removed, err)
	}
	update(t, s, func(db *domain.Database) {
		src := db.Sources["local"]
		src.Assets = refs
		db.Sources["local"] = src
	})
	if err = s.VerifyBlob(reused); err != nil {
		t.Fatal("blob needed by the commit was removed", err)
	}
}

func TestBlobCollectionAfterCommitIsRateLimited(t *testing.T) {
	s := testStore(t)
	addSource(t, s, "local")
	start := time.Now()
	clock := start.Add(time.Hour)
	s.clock = func() time.Time { return clock }
	touch := func() {
		update(t, s, func(db *domain.Database) {
			src := db.Sources["local"]
			src.Title += "!"
			db.Sources["local"] = src
		})
	}
	first := agedBlob(t, s, 1, start, 48*time.Hour)
	touch()
	if present(s.BlobPath(first.SHA)) {
		t.Fatal("commit did not collect")
	}
	second := agedBlob(t, s, 2, start, 48*time.Hour)
	clock = clock.Add(4 * time.Minute)
	touch()
	if !present(s.BlobPath(second.SHA)) {
		t.Fatal("collection ran within the interval")
	}
	clock = clock.Add(time.Minute)
	touch()
	if present(s.BlobPath(second.SHA)) {
		t.Fatal("collection did not run after the interval")
	}
	clock = clock.Add(time.Hour)
	third := agedBlob(t, s, 3, start, 48*time.Hour)
	if err := s.Update(func(*Tx) error { return domain.ErrConflict }); err == nil {
		t.Fatal("failed transaction committed")
	}
	if !present(s.BlobPath(third.SHA)) {
		t.Fatal("failed transaction collected")
	}
}

func TestOpenCollectsBlobs(t *testing.T) {
	s := testStore(t)
	addSource(t, s, "local")
	now := time.Now()
	unused := agedBlob(t, s, 1, now, 48*time.Hour)
	if _, err := Open(s.Dir()); err != nil {
		t.Fatal(err)
	}
	if present(s.BlobPath(unused.SHA)) {
		t.Fatal("open did not collect")
	}
}

func TestCollectBlobsRefusesUnsafeStores(t *testing.T) {
	for name, spoil := range map[string]func(s *Store) *Store{
		"read-only": func(s *Store) *Store { return s.Scoped([]string{"local"}) },
		"invalid":   func(s *Store) *Store { s.invalid = true; return s },
		"unvalidated": func(s *Store) *Store {
			s.db.Sources["broken"] = domain.Source{ID: "other"}
			return s
		},
		"pre-v21": func(s *Store) *Store { s.db.Version = domain.RevisionsFormat - 1; return s },
		"symlinked blobs": func(s *Store) *Store {
			dir := filepath.Join(s.Dir(), "blobs")
			moved := filepath.Join(s.Dir(), "elsewhere")
			if os.Rename(dir, moved) != nil || os.Symlink(moved, dir) != nil {
				t.Fatal("cannot replace the blobs directory")
			}
			return s
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := testStore(t)
			addSource(t, s, "local")
			now := time.Now()
			unused := agedBlob(t, s, 1, now, 48*time.Hour)
			file := filepath.Join(s.Dir(), "elsewhere", unused.SHA)
			if name != "symlinked blobs" {
				file = s.BlobPath(unused.SHA)
			}
			removed, err := spoil(s).CollectBlobs(now)
			if err == nil || removed != 0 || !present(file) {
				t.Fatal("unsafe store collected", removed, err)
			}
		})
	}
}
