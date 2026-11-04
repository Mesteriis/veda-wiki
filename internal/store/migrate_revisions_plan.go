package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// revisionPlan is the converted state MigrateRevisions writes, computed and
// checked before anything changes on disk.
type revisionPlan struct {
	db     domain.Database
	kept   map[string][]int
	report MigrationReport
}

// planRevisions converts original to format 21 in memory: it prunes every
// article by the retention rule, binds each kept revision and publication copy
// without images to the images of its source that its text references
// (domain.RevisionAssets; references the source cannot resolve are left out),
// and checks the result independently of the rule's implementation.
func planRevisions(original domain.Database, updated string) (revisionPlan, error) {
	db := clone(&original)
	removed := domain.PruneAll(&db)
	for key, a := range db.Articles {
		source := db.Sources[a.SourceID]
		if a.Versions != nil {
			versions := make([]domain.Revision, len(a.Versions))
			for i, r := range a.Versions {
				if len(r.Assets) == 0 {
					r.Assets = domain.RevisionAssets(r.Text, a.Path, source)
				}
				versions[i] = r
			}
			a.Versions = versions
		}
		if a.Published != nil && len(a.Published.Assets) == 0 {
			published := *a.Published
			published.Assets = domain.RevisionAssets(published.Text, a.Path, source)
			a.Published = &published
		}
		db.Articles[key] = a
	}
	db.Version = domain.RevisionsFormat
	db.Updated = updated
	plan := revisionPlan{db: db, kept: map[string][]int{}}
	total := 0
	for key, a := range db.Articles {
		before := original.Articles[key]
		numbers := make([]int, 0, len(a.Versions))
		for _, r := range a.Versions {
			numbers = append(numbers, r.Number)
		}
		// The newest KeepRevisions revisions are kept, and every kept
		// revision is an original one with the same number, text and SHA.
		for i := max(0, len(before.Versions)-domain.KeepRevisions); i < len(before.Versions); i++ {
			if !slices.Contains(numbers, before.Versions[i].Number) {
				return revisionPlan{}, errors.New("retention would remove a recent revision")
			}
		}
		for _, r := range a.Versions {
			old, ok := domain.RevisionByNumber(before, r.Number)
			if !ok || old.Text != r.Text || old.SHA != r.SHA || domain.Digest([]byte(r.Text)) != r.SHA {
				return revisionPlan{}, errors.New("retention changed a revision")
			}
		}
		plan.kept[key] = numbers
		total += len(before.Versions)
		plan.report.KeptRevisions += len(numbers)
	}
	plan.report.Articles = len(db.Articles)
	plan.report.RemovedRevisions = removed
	if len(db.Articles) != len(original.Articles) || total != plan.report.KeptRevisions+removed {
		return revisionPlan{}, errors.New("retention counts do not add up")
	}
	if err := Validate(&plan.db); err != nil {
		return revisionPlan{}, errors.New("converted store would be invalid: " + err.Error())
	}
	return plan, nil
}

// blobProblems counts distinct missing and damaged images and the articles
// whose revisions reference them; it holds no names or digests.
type blobProblems struct {
	missing, damaged, articles int
}

// checkRevisionBlobs checks every image a stored revision or publication
// copy of db references with VerifyBlob.
func checkRevisionBlobs(s *Store, db domain.Database) blobProblems {
	var p blobProblems
	bad := map[domain.BlobRef]bool{}
	checked := map[domain.BlobRef]bool{}
	for _, a := range db.Articles {
		versions := a.Versions
		if a.Published != nil {
			versions = append(slices.Clone(versions), *a.Published)
		}
		affected := false
		for _, r := range versions {
			for _, ref := range r.Assets {
				if !checked[ref] {
					checked[ref] = true
					if err := s.VerifyBlob(ref); err != nil {
						bad[ref] = true
						if _, statErr := os.Lstat(s.BlobPath(ref.SHA)); os.IsNotExist(statErr) {
							p.missing++
						} else {
							p.damaged++
						}
					}
				}
				affected = affected || bad[ref]
			}
		}
		if affected {
			p.articles++
		}
	}
	return p
}

// verifyMigration rereads the converted store from disk and requires it to
// equal the plan: SQLite integrity, Validate, the kept revision numbers, each
// kept text byte for byte against the original and its SHA, and every image a
// revision references.
func verifyMigration(dir string, sqlite, readOnly bool, original domain.Database, plan revisionPlan) error {
	check, found, err := readStore(dir, readOnly)
	if err != nil {
		return errors.New("converted store cannot be read: " + scrub(err))
	}
	if !found || check.sqlite != sqlite || check.db.Version != domain.RevisionsFormat {
		return errors.New("converted store has the wrong format")
	}
	if sqlite {
		if err = integrityCheckFile(filepath.Join(dir, SQLiteFile), readOnly); err != nil {
			return err
		}
	}
	if err = Validate(&check.db); err != nil {
		return errors.New("converted store is invalid: " + err.Error())
	}
	if len(check.db.Articles) != len(plan.kept) {
		return errors.New("converted store lost or gained articles")
	}
	for key, numbers := range plan.kept {
		a, ok := check.db.Articles[key]
		if !ok || len(a.Versions) != len(numbers) {
			return errors.New("converted store kept other revisions than planned")
		}
		for i, r := range a.Versions {
			old, found := domain.RevisionByNumber(original.Articles[key], r.Number)
			if r.Number != numbers[i] || !found || r.Text != old.Text || r.SHA != old.SHA || domain.Digest([]byte(r.Text)) != r.SHA {
				return errors.New("converted revision differs from the original")
			}
		}
	}
	want, err := json.Marshal(plan.db)
	if err != nil {
		return err
	}
	got, err := json.Marshal(check.db)
	if err != nil {
		return err
	}
	if !bytes.Equal(want, got) {
		return errors.New("converted store differs from the planned state")
	}
	if p := checkRevisionBlobs(check, check.db); p.articles > 0 {
		return fmt.Errorf("converted revisions of %d articles reference images that are missing (%d) or damaged (%d)", p.articles, p.missing, p.damaged)
	}
	return nil
}

// integrityCheckFile runs the full SQLite integrity check on the file p.
func integrityCheckFile(p string, readOnly bool) error {
	db, err := openSQLiteFile(p, readOnly)
	if err != nil {
		return err
	}
	defer db.Close()
	var result string
	if err = db.QueryRow("PRAGMA integrity_check").Scan(&result); err != nil || result != "ok" {
		return errors.New("SQLite integrity check failed")
	}
	return nil
}

// migrateSQLiteRows converts a legacy SQLite file of format from to the row
// layout of state in one transaction: it creates the revision tables, writes
// each article's revisions as rows and its record as a header, and sets the
// format. Records other than articles and the metadata are not touched; the
// set of articles must be the one on disk.
func migrateSQLiteRows(dir string, state domain.Database, from int) error {
	db, err := sqliteDB(dir)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var data []byte
	if err = tx.QueryRow(`SELECT data FROM records WHERE category='meta' AND id='state'`).Scan(&data); err != nil {
		return err
	}
	var meta sqliteMeta
	if err = json.Unmarshal(data, &meta); err != nil || meta.Version != from {
		return errors.New("store changed during the migration")
	}
	if rows, e := revisionRowsLayout(tx, from); e != nil || rows {
		return errSQLiteLayout
	}
	for _, table := range []string{revisionsTable, revisionAssetsTable} {
		if _, err = tx.Exec(table); err != nil {
			return err
		}
	}
	w, err := newRevisionWriter(tx)
	if err != nil {
		return err
	}
	defer w.close()
	save, err := tx.Prepare(`UPDATE records SET data=? WHERE category=? AND id=?`)
	if err != nil {
		return err
	}
	defer save.Close()
	for id, a := range state.Articles {
		if err = w.sync(nil, &a); err != nil {
			return err
		}
		if data, err = json.Marshal(articleHeader(a)); err != nil {
			return err
		}
		if err = one(save.Exec(data, "article", id)); err != nil {
			return err
		}
	}
	var articles int
	if err = tx.QueryRow(`SELECT count(*) FROM records WHERE category='article'`).Scan(&articles); err != nil {
		return err
	}
	if articles != len(state.Articles) {
		return errors.New("store changed during the migration")
	}
	if data, err = json.Marshal(sqliteMeta{state.Version, state.Updated}); err != nil {
		return err
	}
	if err = one(save.Exec(data, "meta", "state")); err != nil {
		return err
	}
	if err = migrateRevisionsFault("commit"); err != nil {
		return err
	}
	return tx.Commit()
}
