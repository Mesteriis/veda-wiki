package store_test

import (
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store"
)

func numberedArticle(numbers ...int) domain.Article {
	a := domain.Article{SourceID: "one", Path: "a.md"}
	for _, n := range numbers {
		text := "# R"
		a.Versions = append(a.Versions, domain.Revision{Number: n, Text: text, SHA: domain.Digest([]byte(text))})
	}
	return a
}

func validateNumbers(numbers ...int) error {
	db := domain.Database{Version: 20,
		Sources:  map[string]domain.Source{"one": {ID: "one", Title: "One", Kind: "wiki", Visibility: "private"}},
		Articles: map[string]domain.Article{"one/a.md": numberedArticle(numbers...)}}
	return store.Validate(&db)
}

func TestValidateRevisionNumbersStrictlyIncrease(t *testing.T) {
	if err := validateNumbers(1, 2, 3); err != nil {
		t.Fatal("contiguous", err)
	}
	if err := validateNumbers(7, 8, 9); err != nil {
		t.Fatal("gapped", err)
	}
	for _, bad := range [][]int{{7, 9, 8}, {7, 7}, {0, 1}, {-1, 2}} {
		if validateNumbers(bad...) == nil {
			t.Fatal("accepted", bad)
		}
	}
}
