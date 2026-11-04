package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// sqliteStore returns a v21 SQLite store with the private source "local".
func sqliteStore(t *testing.T) *Store {
	t.Helper()
	s := testStore(t)
	addSource(t, s, "local")
	if err := MigrateSQLite(s.Dir()); err != nil {
		t.Fatal(err)
	}
	s, err := Open(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func asset(name string) domain.BlobRef {
	return domain.BlobRef{SHA: domain.Digest([]byte(name)), MIME: "image/png"}
}

func numbered(n int, assets map[string]domain.BlobRef) domain.Revision {
	text := "# Revision " + strconv.Itoa(n)
	return domain.Revision{Number: n, Text: text, SHA: domain.Digest([]byte(text)), Created: "2026-11-04T00:00:00Z", Assets: assets}
}

// writeNext appends the next revision of local/<p> in a commit limited to
// that article; it binds image a.png to the revision.
func writeNext(t *testing.T, s *Store, p string) {
	t.Helper()
	key := "local/" + p
	if err := s.Update(func(tx *Tx) error {
		a := tx.DB().Articles[key]
		a.SourceID, a.Path = "local", p
		n := domain.LatestRevision(a) + 1
		a.Versions = append(slices.Clone(a.Versions), numbered(n, map[string]domain.BlobRef{"a.png": asset("a" + strconv.Itoa(n))}))
		tx.DB().Articles[key] = a
		tx.OnlyArticle(key)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func count(t testing.TB, s *Store, query string, args ...any) int {
	t.Helper()
	db, err := sqliteDB(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err = db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func execSQL(t testing.TB, dir string, statements ...string) {
	t.Helper()
	db, err := sqliteDB(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range statements {
		if _, err = db.Exec(statement); err != nil {
			t.Fatal(statement, err)
		}
	}
}

func TestSQLiteRevisionRowsRoundTrip(t *testing.T) {
	s := sqliteStore(t)
	shared := asset("shared")
	article := domain.Article{SourceID: "local", Path: "guide.md", Versions: []domain.Revision{
		numbered(7, nil),
		numbered(8, map[string]domain.BlobRef{"a.png": shared, "b.png": shared}),
		numbered(9, map[string]domain.BlobRef{"a.png": asset("new"), "c.png": shared}),
	}}
	published := numbered(0, map[string]domain.BlobRef{"a.png": shared})
	article.Published = &published
	update(t, s, func(db *domain.Database) { db.Articles["local/guide.md"] = article })
	reopened, err := Open(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(article)
	got, _ := json.Marshal(reopened.db.Articles["local/guide.md"])
	if string(want) != string(got) {
		t.Fatalf("round trip\nwant %s\ngot  %s", want, got)
	}
	if n := count(t, s, `SELECT count(*) FROM revisions WHERE source_id='local' AND path='guide.md'`); n != 3 {
		t.Fatal("revision rows", n)
	}
	if n := count(t, s, `SELECT count(*) FROM revision_assets WHERE source_id='local' AND path='guide.md'`); n != 3 {
		t.Fatal("revision asset rows", n)
	}
	if n := count(t, s, `SELECT count(*) FROM revision_assets WHERE sha256=? AND number=8`, shared.SHA); n != 1 {
		t.Fatal("shared image rows of revision 8", n)
	}
	if n := count(t, s, `SELECT count(*) FROM records WHERE category='article' AND instr(data, '# Revision 8')>0`); n != 0 {
		t.Fatal("article header stores revision text")
	}
}

// auditRevisions counts every later insert, update and delete of a revision row.
func auditRevisions(t testing.TB, dir string) {
	t.Helper()
	execSQL(t, dir,
		`CREATE TABLE revision_audit (op TEXT NOT NULL, number INTEGER NOT NULL)`,
		`CREATE TRIGGER revision_audit_insert AFTER INSERT ON revisions BEGIN INSERT INTO revision_audit VALUES('insert', NEW.number); END`,
		`CREATE TRIGGER revision_audit_update AFTER UPDATE ON revisions BEGIN INSERT INTO revision_audit VALUES('update', NEW.number); END`,
		`CREATE TRIGGER revision_audit_delete AFTER DELETE ON revisions BEGIN INSERT INTO revision_audit VALUES('delete', OLD.number); END`)
}

func audits(t testing.TB, s *Store) string {
	t.Helper()
	db, err := sqliteDB(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT op, number FROM revision_audit ORDER BY op, number`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var op string
		var n int
		if err = rows.Scan(&op, &n); err != nil {
			t.Fatal(err)
		}
		out = append(out, op+" "+strconv.Itoa(n))
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	execSQL(t, s.Dir(), `DELETE FROM revision_audit`)
	return strings.Join(out, ", ")
}

func TestSQLiteCommitWritesOnlyChangedRevisionRows(t *testing.T) {
	s := sqliteStore(t)
	for range 4 {
		writeNext(t, s, "guide.md")
	}
	writeNext(t, s, "other.md")
	auditRevisions(t, s.Dir())
	writeNext(t, s, "guide.md")
	if got := audits(t, s); got != "delete 2, insert 5" {
		t.Fatal("article commit wrote revision rows", got)
	}
	if n := count(t, s, `SELECT count(*) FROM revisions WHERE source_id='local' AND path='guide.md'`); n != 3 {
		t.Fatal("revision rows after the fifth write", n)
	}
	if n := count(t, s, `SELECT count(*) FROM revision_assets WHERE source_id='local' AND path='guide.md'`); n != 3 {
		t.Fatal("pruned revision kept its image rows", n)
	}
	// A whole-state commit diffs every article: one new row, one pruned row.
	update(t, s, func(db *domain.Database) {
		a := db.Articles["local/guide.md"]
		a.Versions = append(slices.Clone(a.Versions), numbered(6, nil))
		db.Articles["local/guide.md"] = a
	})
	if got := audits(t, s); got != "delete 3, insert 6" {
		t.Fatal("whole-state commit wrote revision rows", got)
	}
	update(t, s, func(db *domain.Database) { delete(db.Articles, "local/guide.md") })
	if got := audits(t, s); got != "delete 4, delete 5, delete 6" {
		t.Fatal("article deletion left revision rows", got)
	}
	if n := count(t, s, `SELECT count(*) FROM revision_assets WHERE path='guide.md'`); n != 0 {
		t.Fatal("article deletion left image rows", n)
	}
	reopened, err := Open(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if got := len(reopened.db.Articles["local/other.md"].Versions); got != 1 {
		t.Fatal("other article lost its revision", got)
	}
}

func TestSQLiteDamagedRevisionRowsFailClosed(t *testing.T) {
	for name, damage := range map[string]string{
		"text":           `UPDATE revisions SET data=json_set(data,'$.text','# Tampered') WHERE number=2`,
		"sha":            `UPDATE revisions SET data=json_set(data,'$.sha256','` + domain.Digest([]byte("x")) + `') WHERE number=2`,
		"number":         `UPDATE revisions SET data=json_set(data,'$.revision',7) WHERE number=2`,
		"missing row":    `DELETE FROM revisions WHERE number=2`,
		"orphan row":     `INSERT INTO revisions SELECT source_id,path,9,json_set(data,'$.revision',9) FROM revisions WHERE number=2`,
		"orphan article": `INSERT INTO revisions SELECT source_id,'gone.md',number,data FROM revisions WHERE number=2`,
		"header text":    `UPDATE records SET data=json_set(data,'$.versions[0].text','# Revision 1') WHERE category='article'`,
		"missing image":  `DELETE FROM revision_assets WHERE number=2`,
		"extra image":    `INSERT INTO revision_assets SELECT '` + domain.Digest([]byte("x")) + `',source_id,path,number FROM revision_assets WHERE number=2`,
		"invalid json":   `UPDATE revisions SET data='{' WHERE number=2`,
	} {
		t.Run(name, func(t *testing.T) {
			s := sqliteStore(t)
			writeNext(t, s, "guide.md")
			writeNext(t, s, "guide.md")
			// A rejected commit makes the store reload the damaged rows.
			execSQL(t, s.Dir(), damage, `CREATE TRIGGER reject_article BEFORE UPDATE ON records WHEN NEW.category='article' BEGIN SELECT RAISE(ABORT, 'fixture'); END`)
			if _, err := Open(s.Dir()); err == nil || err.Error() != "invalid SQLite record" {
				t.Fatal("damaged revision rows opened", err)
			}
			if err := s.Update(func(tx *Tx) error {
				appendRevision(tx.DB(), "local", "guide.md", "# Next")
				tx.OnlyArticle("local/guide.md")
				return nil
			}); err == nil {
				t.Fatal("failed commit accepted")
			}
			if !s.Invalid() {
				t.Fatal("recovery accepted damaged revision rows")
			}
		})
	}
}

func TestSQLiteLayoutMustMatchFormat(t *testing.T) {
	v21 := func(t *testing.T) string { s := sqliteStore(t); writeNext(t, s, "guide.md"); return s.Dir() }
	v20 := func(t *testing.T) string { return legacySQLite(t) }
	for name, tc := range map[string]struct {
		dir    func(*testing.T) string
		damage []string
	}{
		"v21 without revision tables": {v21, []string{`DROP TABLE revision_assets`, `DROP TABLE revisions`}},
		"v21 without revision_assets": {v21, []string{`DROP TABLE revision_assets`}},
		"v20 with revision tables":    {v20, []string{revisionsTable, revisionAssetsTable}},
	} {
		t.Run(name, func(t *testing.T) {
			dir := tc.dir(t)
			execSQL(t, dir, tc.damage...)
			if _, err := Open(dir); err == nil {
				t.Fatal("store layout does not match its format but opened")
			}
		})
	}
}

// legacySQLite migrates a v20 JSON store to SQLite; it keeps the legacy
// layout with revision texts inside the article records.
func legacySQLite(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(domain.Database{Version: 20, Sources: map[string]domain.Source{"local": {ID: "local", Title: "local", Kind: "wiki", Visibility: "private"}}, Articles: map[string]domain.Article{}})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "state.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	if err = MigrateSQLite(dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSQLiteLegacyLayoutLoadsAndWrites(t *testing.T) {
	fixture := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(fixture, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"state.json", SQLiteFile} {
		data, err := os.ReadFile(filepath.Join("testdata", "fc35bea", name))
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(fixture, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for name, dir := range map[string]string{"fc35bea fixture": fixture, "v20": legacySQLite(t)} {
		t.Run(name, func(t *testing.T) {
			s, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			if s.db.Version >= domain.RevisionsFormat {
				t.Fatal("legacy store version", s.db.Version)
			}
			sourceID := "local"
			if name == "fc35bea fixture" {
				sourceID = "fixture"
			}
			update(t, s, func(db *domain.Database) { appendRevision(db, sourceID, "guide.md", "# Legacy write") })
			reopened, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			if text := latest(t, reopened, sourceID+"/guide.md"); text != "# Legacy write" {
				t.Fatal("legacy write lost", text)
			}
			if n := count(t, s, `SELECT count(*) FROM sqlite_master WHERE name IN ('revisions','revision_assets')`); n != 0 {
				t.Fatal("legacy store gained revision tables", n)
			}
			if n := count(t, s, `SELECT count(*) FROM records WHERE category='article' AND instr(data, '# Legacy write')>0`); n != 1 {
				t.Fatal("legacy article record lacks its texts", n)
			}
		})
	}
}

// rebind replaces the images of retained revision 2 of local/guide.md, as an
// import refresh rebinding a revision's images does.
func rebind(tx *Tx, narrow bool) {
	a := tx.DB().Articles["local/guide.md"]
	a.Versions = slices.Clone(a.Versions)
	for i, r := range a.Versions {
		if r.Number == 2 {
			r.Assets = map[string]domain.BlobRef{"a.png": asset("rebound"), "b.png": asset("second")}
			a.Versions[i] = r
		}
	}
	tx.DB().Articles["local/guide.md"] = a
	if narrow {
		tx.OnlyArticle("local/guide.md")
	}
}

func digestsOf(t *testing.T, s *Store, number int) []string {
	t.Helper()
	db, err := sqliteDB(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT sha256 FROM revision_assets WHERE source_id='local' AND path='guide.md' AND number=? ORDER BY sha256`, number)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var sha string
		if err = rows.Scan(&sha); err != nil {
			t.Fatal(err)
		}
		out = append(out, sha)
	}
	return out
}

func TestSQLiteCommitUpdatesChangedRevisionRow(t *testing.T) {
	for name, narrow := range map[string]bool{"whole state": false, "article": true} {
		t.Run(name, func(t *testing.T) {
			s := sqliteStore(t)
			for range 3 {
				writeNext(t, s, "guide.md")
			}
			auditRevisions(t, s.Dir())
			if err := s.Update(func(tx *Tx) error { rebind(tx, narrow); return nil }); err != nil {
				t.Fatal(err)
			}
			if got := audits(t, s); got != "update 2" {
				t.Fatal("rebinding images wrote revision rows", got)
			}
			want := []string{asset("rebound").SHA, asset("second").SHA}
			slices.Sort(want)
			if got := digestsOf(t, s, 2); !slices.Equal(got, want) {
				t.Fatal("image rows of revision 2", got)
			}
			reopened, err := Open(s.Dir())
			if err != nil {
				t.Fatal(err)
			}
			if got := reopened.db.Articles["local/guide.md"].Versions[1].Assets["a.png"]; got != asset("rebound") {
				t.Fatal("rebound image lost", got)
			}
		})
	}
}

// A revision or image row missing behind the open store makes the update or
// delete change no row; the commit fails and recovery rejects the damage
// instead of the commit hiding it.
func TestSQLiteCommitRequiresStoredRevisionRows(t *testing.T) {
	for name, tc := range map[string]struct {
		damage string
		change func(tx *Tx)
	}{
		"updated row":   {`DELETE FROM revisions WHERE number=2`, func(tx *Tx) { rebind(tx, true) }},
		"updated image": {`DELETE FROM revision_assets WHERE number=2`, func(tx *Tx) { rebind(tx, true) }},
		"pruned row": {`DELETE FROM revisions WHERE number=1`, func(tx *Tx) {
			a := tx.DB().Articles["local/guide.md"]
			a.Versions = append(slices.Clone(a.Versions), numbered(4, nil))
			tx.DB().Articles["local/guide.md"] = a
			tx.OnlyArticle("local/guide.md")
		}},
	} {
		t.Run(name, func(t *testing.T) {
			s := sqliteStore(t)
			for range 3 {
				writeNext(t, s, "guide.md")
			}
			execSQL(t, s.Dir(), tc.damage)
			if err := s.Update(func(tx *Tx) error { tc.change(tx); return nil }); err != errRevisionDrift {
				t.Fatal("commit over a missing row", err)
			}
			if !s.Invalid() {
				t.Fatal("recovery accepted the damaged rows")
			}
		})
	}
}

func TestSQLiteLegacyFileRejectsRowFormatCommit(t *testing.T) {
	s, err := Open(legacySQLite(t))
	if err != nil {
		t.Fatal(err)
	}
	update(t, s, func(db *domain.Database) { appendRevision(db, "local", "guide.md", "# Legacy") })
	for name, narrow := range map[string]bool{"whole state": false, "article": true} {
		t.Run(name, func(t *testing.T) {
			err := s.Update(func(tx *Tx) error {
				tx.DB().Version = domain.RevisionsFormat
				appendRevision(tx.DB(), "local", "guide.md", "# Raised")
				if narrow {
					tx.OnlyArticle("local/guide.md")
				}
				return nil
			})
			if err != errSQLiteLayout {
				t.Fatal("legacy file accepted a format 21 commit", err)
			}
			if s.Invalid() || s.db.Version != 20 || latest(t, s, "local/guide.md") != "# Legacy" {
				t.Fatal("failed commit changed memory or closed the store", s.db.Version)
			}
			reopened, err := Open(s.Dir())
			if err != nil || reopened.db.Version != 20 || latest(t, reopened, "local/guide.md") != "# Legacy" {
				t.Fatal("failed commit changed disk", err)
			}
		})
	}
}
