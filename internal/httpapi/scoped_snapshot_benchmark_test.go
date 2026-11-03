package httpapi

import (
	"fmt"
	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store"
	"strings"
	"testing"
)

func BenchmarkScopedSnapshotLargeDocuments(b *testing.B) {
	s := &Store{Store: store.Detached("", domain.Database{Sources: map[string]domain.Source{"docs": {ID: "docs", Kind: "wiki", Visibility: "private"}}, Articles: map[string]domain.Article{}})}
	text := "# Operations\n" + strings.Repeat("container vps service documentation\n", 3500)
	for i := 0; i < 200; i++ {
		p := fmt.Sprintf("%03d.md", i)
		s.DB().Articles["docs/"+p] = domain.Article{SourceID: "docs", Path: p, Versions: []domain.Revision{{Number: 1, Text: text}}}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		view, valid, err := s.ScopedWhere(func(*domain.Database) []string { return []string{"docs"} })
		if err != nil || !valid || len(view.DB().Articles) != 200 {
			b.Fatal("snapshot failed")
		}
	}
}
