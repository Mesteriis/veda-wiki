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
// individual revision, not initial import or migration.
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
	text := "# Operations\n" + strings.Repeat("container vps service documentation\n", 3500)
	sha := domain.Digest([]byte(text))
	if err = s.Update(func(tx *Tx) error {
		for i := 0; i < 200; i++ {
			p := fmt.Sprintf("%03d.md", i)
			tx.DB().Articles["docs/"+p] = domain.Article{SourceID: "docs", Path: p, Versions: []domain.Revision{{Number: 1, Text: text, SHA: sha}}}
		}
		return nil
	}); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// A local article edit, committed as the owner API commits it.
		if err = s.Update(func(tx *Tx) error {
			_, key, err := domain.PutArticle(tx.DB(), "docs", "000.md", text, i+1, time.Now())
			if key != "" {
				tx.OnlyArticle(key)
			}
			return err
		}); err != nil {
			b.Fatal(err)
		}
	}
}
