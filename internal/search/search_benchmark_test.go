package search

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

func benchmarkFindArticlesLargeDocuments(b *testing.B, query string, expected int) {
	db := domain.Database{Sources: map[string]domain.Source{"docs": {ID: "docs", Kind: "wiki", Visibility: "private"}}, Articles: map[string]domain.Article{}}
	text := "# Operations\n" + strings.Repeat("container vps service documentation\n", 3500)
	for i := 0; i < 200; i++ {
		p := fmt.Sprintf("docs/%03d.md", i)
		db.Articles[p] = domain.Article{SourceID: "docs", Path: p[len("docs/"):], Versions: []domain.Revision{{Number: 1, Text: text}}}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if len(Find(db, query, "", false, time.Now())) != expected {
			b.Fatal("search missed fixture")
		}
	}
}

func BenchmarkFindArticlesLargeDocuments(b *testing.B) {
	benchmarkFindArticlesLargeDocuments(b, "container vps", 200)
}
func BenchmarkFindArticlesLargeDocumentsNoExactPhrase(b *testing.B) {
	benchmarkFindArticlesLargeDocuments(b, "container proxmox", 0)
}

func BenchmarkFindArticlesLargeRussianDocumentsNoExactPhrase(b *testing.B) {
	db := domain.Database{Sources: map[string]domain.Source{"infra": {ID: "infra", Kind: "wiki", Visibility: "private"}}, Articles: map[string]domain.Article{}}
	text := "# Инфраструктура\n" + strings.Repeat("Контейнер VPS настройка сервиса\n", 2500)
	for i := 0; i < 200; i++ {
		p := fmt.Sprintf("infra/%03d.md", i)
		db.Articles[p] = domain.Article{SourceID: "infra", Path: p[len("infra/"):], Versions: []domain.Revision{{Number: 1, Text: text}}}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if len(Find(db, "контейнер proxmox", "", false, time.Now())) != 0 {
			b.Fatal("unexpected search result")
		}
	}
}
