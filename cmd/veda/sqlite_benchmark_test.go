package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// The fixture is built in one transaction so benchmark time covers only an
// individual revision, not initial import or migration.
func BenchmarkSQLiteSingleArticleUpdateLargeCorpus(b *testing.B) {
	dir := filepath.Join(b.TempDir(), "data")
	s, err := openStore(dir)
	if err != nil {
		b.Fatal(err)
	}
	if err = s.addSource(Source{ID: "docs", Title: "Docs", Kind: "wiki", Visibility: "private"}); err != nil {
		b.Fatal(err)
	}
	if err = migrateSQLite(dir); err != nil {
		b.Fatal(err)
	}
	s, err = openStore(dir)
	if err != nil {
		b.Fatal(err)
	}
	state := s.Clone()
	text := "# Operations\n" + strings.Repeat("container vps service documentation\n", 3500)
	sha := digest([]byte(text))
	for i := 0; i < 200; i++ {
		p := fmt.Sprintf("%03d.md", i)
		state.Articles["docs/"+p] = Article{SourceID: "docs", Path: p, Versions: []Revision{{Number: 1, Text: text, SHA: sha}}}
	}
	if err = s.Commit(state); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err = s.put("docs", "000.md", text, i+1); err != nil {
			b.Fatal(err)
		}
	}
}
