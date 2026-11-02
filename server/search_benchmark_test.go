package main

import (
	"fmt"
	"strings"
	"testing"
)

func BenchmarkFindArticlesLargeDocuments(b *testing.B) {
	s := &Store{db: Database{Sources: map[string]Source{"docs": {ID: "docs", Kind: "wiki", Visibility: "private"}}, Articles: map[string]Article{}}}
	text := "# Operations\n" + strings.Repeat("container vps service documentation\n", 3500)
	for i := 0; i < 200; i++ {
		p := fmt.Sprintf("docs/%03d.md", i)
		s.db.Articles[p] = Article{SourceID: "docs", Path: p[len("docs/"):], Versions: []Revision{{Number: 1, Text: text}}}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if len(s.findArticles("container vps", "", false)) != 200 {
			b.Fatal("search missed fixture")
		}
	}
}
