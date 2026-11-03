package main

import (
	"encoding/json"
	"github.com/Mesteriis/veda-wiki/internal/render"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGraphUsesVisibleMarkdownLinksOnly(t *testing.T) {
	s := testStore(t)
	source(t, s, "public", "public")
	source(t, s, "private", "private")
	for _, item := range []struct{ source, name, text string }{
		{"public", "guide.md", "# Guide\n\n[Target](docs/) [Again](docs/) [outside](https://example.com) ![image](image.png) [escape](../private/secret.md)"},
		{"public", "docs/README.md", "# Docs"},
		{"private", "secret.md", "# Secret\n\n[Private target](target.md)"},
		{"private", "target.md", "# Private target"},
	} {
		if _, err := s.put(item.source, item.name, item.text, 0); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		public bool
		want   int
	}{{true, 1}, {false, 2}} {
		w := Wiki{Store: s, Token: "test-owner", Public: tc.public}
		r := request(w, "GET", "/api/state?graph=1", "", !tc.public)
		if r.Code != 200 {
			t.Fatalf("status: %d", r.Code)
		}
		var result struct {
			View struct{ GraphEdges []render.Edge }
		}
		if err := json.Unmarshal(r.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if len(result.View.GraphEdges) != tc.want {
			t.Fatalf("public=%v edges=%+v", tc.public, result.View.GraphEdges)
		}
		if result.View.GraphEdges[0] != (render.Edge{From: "public/guide.md", To: "public/docs/README.md"}) && tc.public {
			t.Fatal(result.View.GraphEdges)
		}
	}
}

func TestGraphRespectsScopedReader(t *testing.T) {
	s := testStore(t)
	for _, id := range []string{"alpha", "beta"} {
		source(t, s, id, "private")
		if _, err := s.put(id, "guide.md", "# Guide\n\n[Target](target.md)", 0); err != nil {
			t.Fatal(err)
		}
		if _, err := s.put(id, "target.md", "# Target", 0); err != nil {
			t.Fatal(err)
		}
	}
	key, err := s.auth().CreateAccessKey("reader", []string{"alpha"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/api/state?graph=1", nil)
	r.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	(Wiki{Store: s, Token: "test-owner"}).ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "alpha/target.md") || strings.Contains(w.Body.String(), "beta/") {
		t.Fatal("graph revealed an out-of-scope source", w.Code)
	}
}
