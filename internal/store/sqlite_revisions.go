package store

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// From format 21 (domain.RevisionsFormat) each article revision is its own
// row, so a write inserts the new revision and deletes the pruned ones instead
// of rewriting the whole history. The article record keeps a header: every
// field, with Versions holding number, SHA, time and images but no text.
// revision_assets mirrors the images (Revision.Assets) of every stored
// revision by digest. Stores below format 21 keep the texts inside the article
// records and have neither table; the tables exist exactly when the store
// format is 21 or later.
const (
	revisionsTable      = `CREATE TABLE revisions (source_id TEXT NOT NULL, path TEXT NOT NULL, number INTEGER NOT NULL, data BLOB NOT NULL, PRIMARY KEY (source_id,path,number)) WITHOUT ROWID`
	revisionAssetsTable = `CREATE TABLE revision_assets (sha256 TEXT NOT NULL, source_id TEXT NOT NULL, path TEXT NOT NULL, number INTEGER NOT NULL, PRIMARY KEY (sha256,source_id,path,number)) WITHOUT ROWID`
)

var errSQLiteLayout = errors.New("SQLite layout does not match the store format")

type rowQuerier interface {
	QueryRow(query string, args ...any) *sql.Row
}

// revisionRowsLayout reports whether the SQLite file stores revisions as rows
// and requires that layout to match the store format.
func revisionRowsLayout(q rowQuerier, version int) (bool, error) {
	var tables int
	if err := q.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('revisions','revision_assets')`).Scan(&tables); err != nil {
		return false, err
	}
	rows := tables == 2
	if tables == 1 || rows != (version >= domain.RevisionsFormat) {
		return false, errSQLiteLayout
	}
	return rows, nil
}

// articleHeader returns a without revision texts. It copies Versions.
func articleHeader(a domain.Article) domain.Article {
	if a.Versions == nil {
		return a
	}
	versions := make([]domain.Revision, len(a.Versions))
	for i, r := range a.Versions {
		r.Text = ""
		versions[i] = r
	}
	a.Versions = versions
	return a
}

// assetDigests returns the distinct image digests of r.
func assetDigests(r domain.Revision) map[string]bool {
	out := make(map[string]bool, len(r.Assets))
	for _, ref := range r.Assets {
		out[ref.SHA] = true
	}
	return out
}

type revisionKey struct {
	source, path string
	number       int
}

// attachRevisions fills the revision texts of the article headers from the
// revision rows. Every header revision needs exactly one row and every row a
// header revision; the row must equal the header revision apart from its
// text, its text must match its SHA and its image rows must match its images.
func attachRevisions(db *sql.DB, articles map[string]domain.Article) error {
	invalid := errors.New("invalid SQLite record")
	stored := map[revisionKey]domain.Revision{}
	rows, err := db.Query(`SELECT source_id,path,number,data FROM revisions`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var k revisionKey
		var data []byte
		if err = rows.Scan(&k.source, &k.path, &k.number, &data); err != nil {
			return err
		}
		var r domain.Revision
		if json.Unmarshal(data, &r) != nil || r.Number != k.number || domain.Digest([]byte(r.Text)) != r.SHA {
			return invalid
		}
		stored[k] = r
	}
	if err = rows.Err(); err != nil {
		return err
	}
	images := map[revisionKey]map[string]bool{}
	assetRows, err := db.Query(`SELECT sha256,source_id,path,number FROM revision_assets`)
	if err != nil {
		return err
	}
	defer assetRows.Close()
	for assetRows.Next() {
		var sha string
		var k revisionKey
		if err = assetRows.Scan(&sha, &k.source, &k.path, &k.number); err != nil {
			return err
		}
		if images[k] == nil {
			images[k] = map[string]bool{}
		}
		images[k][sha] = true
	}
	if err = assetRows.Err(); err != nil {
		return err
	}
	for id, a := range articles {
		if a.Versions == nil {
			continue
		}
		versions := make([]domain.Revision, len(a.Versions))
		for i, header := range a.Versions {
			k := revisionKey{a.SourceID, a.Path, header.Number}
			r, ok := stored[k]
			if !ok || !sameHeader(header, r) || !sameDigests(assetDigests(r), images[k]) {
				return invalid
			}
			delete(stored, k)
			delete(images, k)
			versions[i] = r
		}
		a.Versions = versions
		articles[id] = a
	}
	if len(stored) != 0 || len(images) != 0 {
		return invalid
	}
	return nil
}

// sameHeader reports whether header is r without its text. Via is not
// compared: a binary that predates it rewrites an article header without the
// field while the row keeps it, and the row is the authority.
func sameHeader(header, r domain.Revision) bool {
	r.Text, r.Via, header.Via = "", "", ""
	a, errA := json.Marshal(header)
	b, errB := json.Marshal(r)
	return errA == nil && errB == nil && bytes.Equal(a, b)
}

func sameDigests(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for sha := range a {
		if !b[sha] {
			return false
		}
	}
	return true
}

// revisionWriter changes revision rows inside one SQLite transaction.
type revisionWriter struct {
	tx                                        *sql.Tx
	insert, update, remove, addImage, dropImg *sql.Stmt
}

func newRevisionWriter(tx *sql.Tx) (*revisionWriter, error) {
	w := &revisionWriter{tx: tx}
	statements := []struct {
		stmt  **sql.Stmt
		query string
	}{
		{&w.insert, `INSERT INTO revisions(source_id,path,number,data) VALUES(?,?,?,?)`},
		{&w.update, `UPDATE revisions SET data=? WHERE source_id=? AND path=? AND number=?`},
		{&w.remove, `DELETE FROM revisions WHERE source_id=? AND path=? AND number=?`},
		{&w.addImage, `INSERT INTO revision_assets(sha256,source_id,path,number) VALUES(?,?,?,?)`},
		{&w.dropImg, `DELETE FROM revision_assets WHERE sha256=? AND source_id=? AND path=? AND number=?`},
	}
	for _, s := range statements {
		stmt, err := tx.Prepare(s.query)
		if err != nil {
			w.close()
			return nil, err
		}
		*s.stmt = stmt
	}
	return w, nil
}

func (w *revisionWriter) close() {
	for _, stmt := range []*sql.Stmt{w.insert, w.update, w.remove, w.addImage, w.dropImg} {
		if stmt != nil {
			stmt.Close()
		}
	}
}

// storedHeader returns the article header the store holds under id, or nil.
func (w *revisionWriter) storedHeader(id string) (*domain.Article, error) {
	var data []byte
	err := w.tx.QueryRow(`SELECT data FROM records WHERE category='article' AND id=?`, id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var a domain.Article
	if err = json.Unmarshal(data, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// storedHeaders returns every article header the store holds, by id.
func (w *revisionWriter) storedHeaders() (map[string]domain.Article, error) {
	rows, err := w.tx.Query(`SELECT id,data FROM records WHERE category='article'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]domain.Article{}
	for rows.Next() {
		var id string
		var data []byte
		if err = rows.Scan(&id, &data); err != nil {
			return nil, err
		}
		var a domain.Article
		if err = json.Unmarshal(data, &a); err != nil {
			return nil, err
		}
		out[id] = a
	}
	return out, rows.Err()
}

// sync writes the difference between the stored header old (nil for a new
// article) and article (nil for a removed one). Rows of revisions whose
// header is unchanged are not touched; the stored rows match the stored
// header because the loader requires it and every commit keeps it.
func (w *revisionWriter) sync(old, article *domain.Article) error {
	stored := map[int]domain.Revision{}
	if old != nil {
		if article != nil && (old.SourceID != article.SourceID || old.Path != article.Path) {
			if err := w.sync(old, nil); err != nil {
				return err
			}
		} else {
			for _, r := range old.Versions {
				stored[r.Number] = r
			}
		}
	}
	if article != nil {
		for _, r := range article.Versions {
			previous, ok := stored[r.Number]
			delete(stored, r.Number)
			if ok && sameHeader(previous, r) {
				continue
			}
			data, err := json.Marshal(r)
			if err != nil {
				return err
			}
			if ok {
				if err = w.dropImages(old.SourceID, old.Path, previous); err != nil {
					return err
				}
				err = one(w.update.Exec(data, article.SourceID, article.Path, r.Number))
			} else {
				_, err = w.insert.Exec(article.SourceID, article.Path, r.Number, data)
			}
			if err != nil {
				return err
			}
			for sha := range assetDigests(r) {
				if _, err = w.addImage.Exec(sha, article.SourceID, article.Path, r.Number); err != nil {
					return err
				}
			}
		}
	}
	for number, r := range stored {
		if err := one(w.remove.Exec(old.SourceID, old.Path, number)); err != nil {
			return err
		}
		if err := w.dropImages(old.SourceID, old.Path, r); err != nil {
			return err
		}
	}
	return nil
}

func (w *revisionWriter) dropImages(source, path string, r domain.Revision) error {
	for sha := range assetDigests(r) {
		if err := one(w.dropImg.Exec(sha, source, path, r.Number)); err != nil {
			return err
		}
	}
	return nil
}

var errRevisionDrift = errors.New("SQLite revision rows do not match the stored article")

// one requires a statement to change exactly one row: an update or delete of
// a row the stored header lists. A row missing on disk fails the commit, so
// recovery reloads instead of a commit leaving a store Open rejects.
func one(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errRevisionDrift
	}
	return nil
}
