package httpapi

import (
	"fmt"
	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store/storetest"
	"strings"
	"testing"
)

func BenchmarkScopedSnapshotLargeDocuments(b *testing.B) {
	s := storetest.New(b)
	text := "# Operations\n" + strings.Repeat("container vps service documentation\n", 3500)
	storetest.Mutate(b, s, func(db *domain.Database) {
		db.Sources["docs"] = domain.Source{ID: "docs", Kind: "wiki", Visibility: "private"}
		for i := 0; i < 200; i++ {
			p := fmt.Sprintf("%03d.md", i)
			db.Articles["docs/"+p] = domain.Article{SourceID: "docs", Path: p, Versions: []domain.Revision{{Number: 1, Text: text}}}
		}
	})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		view, valid, err := s.ScopedWhere(func(*domain.Database) []string { return []string{"docs"} })
		articles := 0
		if err == nil && valid {
			_ = view.View(func(db *domain.Database) error { articles = len(db.Articles); return nil })
		}
		if err != nil || !valid || articles != 200 {
			b.Fatal("snapshot failed")
		}
	}
}
