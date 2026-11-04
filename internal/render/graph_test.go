package render

import (
	"encoding/json"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

func TestEdgeJSONKeys(t *testing.T) {
	b, err := json.Marshal(Edge{From: "a/x.md", To: "a/y.md"})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"from":"a/x.md","to":"a/y.md"}` {
		t.Fatal(string(b))
	}
}

func TestGraphEdgesVisibleLinksOnly(t *testing.T) {
	pages := []domain.Page{
		{Path: "public/guide.md", SearchText: "[T](docs/) [again](docs/) [out](https://example.com) [escape](../private/secret.md) [self](guide.md)"},
		{Path: "public/docs/README.md", SearchText: "# Docs"},
	}
	edges := GraphEdges(pages)
	if len(edges) != 1 || edges[0] != (Edge{From: "public/guide.md", To: "public/docs/README.md"}) {
		t.Fatalf("%+v", edges)
	}
	if got := GraphEdges(nil); got == nil || len(got) != 0 {
		t.Fatal("empty graph must be a non-nil empty slice")
	}
}
