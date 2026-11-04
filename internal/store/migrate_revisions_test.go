package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// The v20-history fixture is synthetic: a format 20 SQLite store in the
// legacy layout (texts inside the article records) with blobs.
//
//   - notes/guide.md: six revisions binding img/a.png and img/b.png in
//     different combinations (revision 6 also names an image the source does
//     not have); its publication copy is revision 4. Kept: 4, 5, 6.
//   - notes/queued.md: five revisions; a pending outbox job holds revision 1.
//     Kept: 1, 3, 4, 5.
//   - notes/moved.md: five revisions; a complete move took revision 2 to
//     archive/moved.md (one revision). Kept: 3, 4, 5.
//   - notes/short.md: two revisions, the second binds img/c.png. Kept: both.
//   - notes/pending.md: five revisions; an active move (waiting_destination)
//     holds revision 1 and the first revision of archive/pending.md.
//     Kept: 1, 3, 4, 5.
//   - notes/based.md: five revisions; archive/based.md has a ReconcileBase on
//     its revision 2. Kept: 2, 3, 4, 5.
//   - hub/doc.md: five revisions in the connected source hub; its
//     UpstreamHash is revision 2. Kept: 2, 3, 4, 5.
const v20Fixture = "v20-history"

// v20Report is the conversion of the v20-history fixture.
var v20Report = MigrationReport{Articles: 10, KeptRevisions: 27, RemovedRevisions: 9}

var v20Kept = map[string][]int{
	"notes/guide.md":     {4, 5, 6},
	"notes/queued.md":    {1, 3, 4, 5},
	"notes/moved.md":     {3, 4, 5},
	"archive/moved.md":   {1},
	"notes/short.md":     {1, 2},
	"notes/pending.md":   {1, 3, 4, 5},
	"archive/pending.md": {1},
	"notes/based.md":     {2, 3, 4, 5},
	"archive/based.md":   {1},
	"hub/doc.md":         {2, 3, 4, 5},
}

// copyFixture copies testdata/name into a private data directory.
func copyFixture(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "data")
	root := filepath.Join("testdata", name)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		target := filepath.Join(dir, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0600)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// toJSON rewrites a SQLite data directory as a JSON store of the same state
// and format, as a store that never moved to SQLite has.
func toJSON(t *testing.T, dir string) string {
	t.Helper()
	s, _, err := read(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(s.db)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(dir, SQLiteFile)); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "state.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// legacyState reads a store below format 21 as MigrateRevisions sees it.
func legacyState(t *testing.T, dir string) domain.Database {
	t.Helper()
	s, found, err := read(dir)
	if err != nil || !found || s.db.Version >= domain.RevisionsFormat {
		t.Fatal("not a legacy store", err)
	}
	return s.db
}

func snapshot(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	for _, name := range []string{"state.json", SQLiteFile} {
		if data, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
			out[name] = data
		}
	}
	return out
}

func sameFiles(t *testing.T, dir string, want map[string][]byte) {
	t.Helper()
	got := snapshot(t, dir)
	if len(got) != len(want) {
		t.Fatal("store files changed")
	}
	for name, data := range want {
		if !bytes.Equal(got[name], data) {
			t.Fatal("store file changed:", name)
		}
	}
}

func noBackups(t *testing.T, dir string) {
	t.Helper()
	if revisionBackupPresent(dir) {
		t.Fatal("refused migration left a backup")
	}
}

// backupsHold requires the backups to be byte copies of the given files.
func backupsHold(t *testing.T, dir string, files map[string][]byte) {
	t.Helper()
	for name, data := range files {
		backup := "state.before-revisions." + strings.TrimPrefix(name, "state.")
		got, err := os.ReadFile(filepath.Join(dir, backup))
		if err != nil || !bytes.Equal(got, data) {
			t.Fatal("backup is not a copy of", name, err)
		}
	}
}

func numbersOf(a domain.Article) []int {
	var out []int
	for _, r := range a.Versions {
		out = append(out, r.Number)
	}
	return out
}

// checkMigrated opens the converted store and compares it with original.
func checkMigrated(t *testing.T, dir string, original domain.Database, kept map[string][]int) *Store {
	t.Helper()
	s, err := Open(dir)
	if err != nil {
		t.Fatal("converted store does not open", err)
	}
	if s.db.Version != domain.RevisionsFormat || len(s.db.Articles) != len(kept) {
		t.Fatal("converted store", s.db.Version, len(s.db.Articles))
	}
	for key, numbers := range kept {
		a := s.db.Articles[key]
		if !slices.Equal(numbersOf(a), numbers) {
			t.Fatal(key, "kept", numbersOf(a), "want", numbers)
		}
		for _, r := range a.Versions {
			old, _ := domain.RevisionByNumber(original.Articles[key], r.Number)
			if r.Text != old.Text || r.SHA != old.SHA || domain.Digest([]byte(r.Text)) != r.SHA {
				t.Fatal(key, r.Number, "text changed")
			}
		}
	}
	return s
}

func imageNames(assets map[string]domain.BlobRef) []string {
	var out []string
	for name := range assets {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

func TestMigrateRevisionsConvertsV20Fixture(t *testing.T) {
	for _, backend := range []string{"sqlite", "json"} {
		t.Run(backend, func(t *testing.T) {
			dir := copyFixture(t, v20Fixture)
			if backend == "json" {
				toJSON(t, dir)
			}
			original, before := legacyState(t, dir), snapshot(t, dir)
			report, err := MigrateRevisions(dir)
			if err != nil {
				t.Fatal(err)
			}
			if report != v20Report {
				t.Fatal("report", report)
			}
			backupsHold(t, dir, before)
			s := checkMigrated(t, dir, original, v20Kept)
			if s.IsSQLite() != (backend == "sqlite") {
				t.Fatal("backend changed")
			}
			guide := s.db.Articles["notes/guide.md"]
			for i, want := range [][]string{{"img/a.png", "img/b.png"}, nil, {"img/b.png"}} {
				if got := imageNames(guide.Versions[i].Assets); !slices.Equal(got, want) {
					t.Fatal("revision", guide.Versions[i].Number, "images", got)
				}
			}
			if got := imageNames(guide.Published.Assets); !slices.Equal(got, []string{"img/a.png", "img/b.png"}) || guide.Published.Text != original.Articles["notes/guide.md"].Published.Text {
				t.Fatal("publication copy", got)
			}
			if got := imageNames(s.db.Articles["notes/short.md"].Versions[1].Assets); !slices.Equal(got, []string{"img/c.png"}) {
				t.Fatal("short images", got)
			}
			if backend == "sqlite" {
				if n := count(t, s, `SELECT count(*) FROM revisions`); n != v20Report.KeptRevisions {
					t.Fatal("revision rows", n)
				}
				if n := count(t, s, `SELECT count(*) FROM records WHERE category='article' AND (instr(data, 'Revision 3')>0 OR instr(data, 'Five, no images')>0)`); n != 0 {
					t.Fatal("article records still hold texts", n)
				}
			}
			entries, err := os.ReadDir(filepath.Join(dir, "blobs"))
			if err != nil || len(entries) != 3 {
				t.Fatal("blobs changed", len(entries), err)
			}
		})
	}
}

func TestMigrateRevisionsConvertsFc35beaFixture(t *testing.T) {
	for _, backend := range []string{"sqlite", "json"} {
		t.Run(backend, func(t *testing.T) {
			dir := copyFixture(t, "fc35bea")
			if backend == "json" {
				toJSON(t, dir)
			}
			original := legacyState(t, dir)
			report, err := MigrateRevisions(dir)
			if err != nil || report != (MigrationReport{Articles: 1, KeptRevisions: 2, RemovedRevisions: 0}) {
				t.Fatal(report, err)
			}
			s := checkMigrated(t, dir, original, map[string][]int{"fixture/guide.md": {1, 2}})
			if len(s.db.Users) != 1 || len(s.db.Teams) != 1 || len(s.db.AccessKeys) != 1 {
				t.Fatal("other records changed")
			}
		})
	}
}

func TestOpenRefusesStoresBelowFormat21(t *testing.T) {
	for fixture, want := range map[string]string{v20Fixture: "v20", "fc35bea": "v15"} {
		dir := copyFixture(t, fixture)
		before := snapshot(t, dir)
		_, err := Open(dir)
		if err == nil || err.Error() != `store format `+want+` requires "veda migrate-revisions"` {
			t.Fatal("old store opened or wrong refusal", err)
		}
		sameFiles(t, dir, before)
	}
}

func TestMigrateRevisionsRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		prepare func(t *testing.T) string
		want    error
		text    string
	}{
		"active writer": {prepare: func(t *testing.T) string {
			dir := copyFixture(t, v20Fixture)
			holdWriterLock(t, dir)
			return dir
		}, want: errMigrationInUse},
		"incomplete sqlite backup": {prepare: func(t *testing.T) string {
			dir := copyFixture(t, v20Fixture)
			writeFile(t, filepath.Join(dir, backupSQLite), "x")
			return dir
		}, text: "the original store was not modified: delete the state.before-revisions.* files"},
		"damaged backup pair": {prepare: func(t *testing.T) string {
			dir := copyFixture(t, v20Fixture)
			writeFile(t, filepath.Join(dir, backupSQLite), "x")
			writeFile(t, filepath.Join(dir, backupJSON), `{"version":14,"backend":"sqlite"}`)
			return dir
		}, text: "the original store was not modified"},
		"complete backup": {prepare: func(t *testing.T) string {
			dir := copyFixture(t, v20Fixture)
			for from, to := range map[string]string{SQLiteFile: backupSQLite, "state.json": backupJSON} {
				data, err := os.ReadFile(filepath.Join(dir, from))
				if err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(dir, to), string(data))
			}
			return dir
		}, text: `run "veda migrate-revisions --verify --data DIR"`},
		"already v21": {prepare: func(t *testing.T) string {
			s := sqliteStore(t)
			writeNext(t, s, "guide.md")
			return s.Dir()
		}, want: errMigrationDone},
		"no store": {prepare: func(t *testing.T) string {
			dir := filepath.Join(t.TempDir(), "data")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			return dir
		}, want: errMigrationNoStore},
		"revision tables in a v20 file": {prepare: func(t *testing.T) string {
			dir := copyFixture(t, v20Fixture)
			execSQL(t, dir, revisionsTable, revisionAssetsTable)
			return dir
		}, want: errMigrationLayout},
		"one revision table in a v20 file": {prepare: func(t *testing.T) string {
			dir := copyFixture(t, v20Fixture)
			execSQL(t, dir, revisionsTable)
			return dir
		}, want: errMigrationLayout},
		"rollback journal left": {prepare: func(t *testing.T) string {
			dir := copyFixture(t, v20Fixture)
			hotJournal(t, dir)
			return dir
		}, want: errMigrationJournal},
		"write-ahead log left": {prepare: func(t *testing.T) string {
			dir := copyFixture(t, v20Fixture)
			writeFile(t, filepath.Join(dir, SQLiteFile+"-wal"), strings.Repeat("w", 64))
			return dir
		}, want: errMigrationJournal},
		"invalid store": {prepare: func(t *testing.T) string {
			dir := copyFixture(t, v20Fixture)
			execSQL(t, dir, `UPDATE records SET data=replace(data, 'Revision 3', 'Revision X') WHERE category='article' AND id='notes/queued.md'`)
			return dir
		}, text: "store is invalid, nothing was changed"},
		"missing image": {prepare: func(t *testing.T) string {
			dir := copyFixture(t, v20Fixture)
			s, _, err := read(dir)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.Remove(s.BlobPath(s.db.Sources["notes"].Assets["img/c.png"].SHA)); err != nil {
				t.Fatal(err)
			}
			writeFile(t, s.BlobPath(s.db.Sources["notes"].Assets["img/b.png"].SHA), "not an image")
			return dir
		}, text: "revisions of 2 articles reference images that are missing (1) or damaged (1), nothing was changed"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := tc.prepare(t)
			before := snapshot(t, dir)
			backups := map[string][]byte{}
			for _, b := range revisionBackups {
				if data, err := os.ReadFile(filepath.Join(dir, b)); err == nil {
					backups[b] = data
				}
			}
			report, err := MigrateRevisions(dir)
			if err == nil || report != (MigrationReport{}) {
				t.Fatal("migration was not refused", report)
			}
			if tc.want != nil && !errors.Is(err, tc.want) || tc.text != "" && !strings.Contains(err.Error(), tc.text) {
				t.Fatal("refusal", err)
			}
			sameFiles(t, dir, before)
			for _, b := range revisionBackups {
				data, err := os.ReadFile(filepath.Join(dir, b))
				if want, ok := backups[b]; ok != (err == nil) || ok && !bytes.Equal(data, want) {
					t.Fatal("backups changed")
				}
			}
		})
	}
}

func writeFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

// hotJournal leaves the store in dir as a process that crashed inside a
// write transaction does: the rollback journal SQLite wrote next to it, and
// the database file as it was at that moment.
func hotJournal(t *testing.T, dir string) {
	t.Helper()
	scratch := filepath.Join(t.TempDir(), "hot")
	if err := os.Mkdir(scratch, 0700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, SQLiteFile))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(scratch, SQLiteFile), string(data))
	db, err := sqliteDB(scratch)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, statement := range []string{"PRAGMA cache_size=1", "BEGIN IMMEDIATE", "UPDATE records SET data=data WHERE category='article'", "CREATE TABLE crash (x BLOB)", "INSERT INTO crash VALUES (zeroblob(65536))"} {
		if _, err = conn.ExecContext(context.Background(), statement); err != nil {
			t.Fatal(statement, err)
		}
	}
	for _, name := range []string{SQLiteFile, SQLiteFile + "-journal"} {
		data, err := os.ReadFile(filepath.Join(scratch, name))
		if err != nil || len(data) == 0 {
			t.Fatal("no hot journal", name, err)
		}
		writeFile(t, filepath.Join(dir, name), string(data))
	}
	if _, err = conn.ExecContext(context.Background(), "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
}
