package store

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// The fixture is built in one transaction so benchmark time covers only an
// individual revision, not initial import or migration. Each of the 500
// articles holds a full retained history (domain.KeepRevisions revisions).
// Triggers count what one article write stores: written-B/op is the record
// and revision data inserted or updated, revision-rows/op the revision rows
// inserted, updated or deleted.
func BenchmarkSQLiteSingleArticleUpdateLargeCorpus(b *testing.B) {
	dir := filepath.Join(b.TempDir(), "data")
	s, err := Open(dir)
	if err != nil {
		b.Fatal(err)
	}
	if err = s.Update(func(tx *Tx) error {
		return domain.AddSource(tx.DB(), domain.Source{ID: "docs", Title: "Docs", Kind: "wiki", Visibility: "private"})
	}); err != nil {
		b.Fatal(err)
	}
	if err = MigrateSQLite(dir); err != nil {
		b.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		b.Fatal(err)
	}
	body := "# Operations\n" + strings.Repeat("container vps service documentation\n", 600)
	if err = s.Update(func(tx *Tx) error {
		for i := 0; i < 500; i++ {
			p := fmt.Sprintf("%03d.md", i)
			var versions []domain.Revision
			for n := 1; n <= domain.KeepRevisions; n++ {
				text := fmt.Sprintf("%s%d\n", body, n)
				versions = append(versions, domain.Revision{Number: n, Text: text, SHA: domain.Digest([]byte(text))})
			}
			tx.DB().Articles["docs/"+p] = domain.Article{SourceID: "docs", Path: p, Versions: versions}
		}
		return nil
	}); err != nil {
		b.Fatal(err)
	}
	audit(b, dir)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// A local article edit, committed as the owner API commits it.
		text := fmt.Sprintf("%sedit %d\n", body, i)
		if err = s.Update(func(tx *Tx) error {
			_, key, err := domain.PutArticle(tx.DB(), "docs", "000.md", text, domain.KeepRevisions+i, time.Now())
			if key != "" {
				tx.OnlyArticle(key)
			}
			return err
		}); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	written, rows := audited(b, dir)
	b.ReportMetric(float64(written)/float64(b.N), "written-B/op")
	b.ReportMetric(float64(rows)/float64(b.N), "revision-rows/op")
}

// audit records the size of every record or revision row written from now on.
func audit(b *testing.B, dir string) {
	b.Helper()
	db, err := sqliteDB(dir)
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	statements := []string{
		`CREATE TABLE audit (kind TEXT NOT NULL, bytes INTEGER NOT NULL)`,
		`CREATE TRIGGER audit_record_insert AFTER INSERT ON records BEGIN INSERT INTO audit VALUES('record', length(NEW.data)); END`,
		`CREATE TRIGGER audit_record_update AFTER UPDATE ON records BEGIN INSERT INTO audit VALUES('record', length(NEW.data)); END`,
	}
	var tables int
	if err = db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='revisions'`).Scan(&tables); err != nil {
		b.Fatal(err)
	}
	if tables == 1 {
		statements = append(statements,
			`CREATE TRIGGER audit_revision_insert AFTER INSERT ON revisions BEGIN INSERT INTO audit VALUES('revision', length(NEW.data)); END`,
			`CREATE TRIGGER audit_revision_update AFTER UPDATE ON revisions BEGIN INSERT INTO audit VALUES('revision', length(NEW.data)); END`,
			`CREATE TRIGGER audit_revision_delete AFTER DELETE ON revisions BEGIN INSERT INTO audit VALUES('revision', 0); END`)
	}
	for _, statement := range statements {
		if _, err = db.Exec(statement); err != nil {
			b.Fatal(err)
		}
	}
}

func audited(b *testing.B, dir string) (written, revisionRows int64) {
	b.Helper()
	db, err := sqliteDB(dir)
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	if err = db.QueryRow(`SELECT coalesce(sum(bytes),0), coalesce(sum(kind='revision'),0) FROM audit`).Scan(&written, &revisionRows); err != nil {
		b.Fatal(err)
	}
	return written, revisionRows
}
