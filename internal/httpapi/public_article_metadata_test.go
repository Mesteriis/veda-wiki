package httpapi

import (
	"strings"
	"testing"
)

func TestPublicLocalArticleHidesImportConflictAndPublicationMetadata(t *testing.T) {
	s := testStore(t)
	b := testBatch()
	b.Visibility = "public"
	if _, err := s.importBatch(b); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("handbook", "README.md", "# Local public", 1); err != nil {
		t.Fatal(err)
	}
	b = applyAgain(s, b)
	b.Files = map[string]string{"README.md": "# Incoming private fixture"}
	b.Commit = strings.Repeat("b", 40)
	if result, err := s.importBatch(b); err != nil || result.Conflicts != 1 {
		t.Fatal("fixture conflict missing", err)
	}
	route := "/api/v1/articles?source_id=handbook&path=README.md"
	out := request(Wiki{Store: s, Public: true}, "GET", route, "", false)
	if out.Code != 200 || !strings.Contains(out.Body.String(), "Local public") {
		t.Fatal("public article missing", out.Code)
	}
	for _, hidden := range []string{"Incoming private fixture", "conflicts", "upstream_sha256", "published_local", `"published"`} {
		if strings.Contains(out.Body.String(), hidden) {
			t.Fatalf("public article exposed %s", hidden)
		}
	}
	owner := request(Wiki{Store: s, Token: "test-owner"}, "GET", route+"&history=1", "", true)
	if owner.Code != 200 || !strings.Contains(owner.Body.String(), "Incoming private fixture") {
		t.Fatal("owner lost conflict metadata", owner.Code)
	}
}
