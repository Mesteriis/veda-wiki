package httpapi

import (
	"encoding/json"
	"fmt"
	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestExcerptScopedUnicodeAndRevision(t *testing.T) {
	s := testStore(t)
	source(t, s, "alpha", "private")
	source(t, s, "beta", "private")
	original := "я😀alpha\nфинал"
	if _, err := s.put("alpha", "guide.md", original, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("beta", "guide.md", "# Other secret", 0); err != nil {
		t.Fatal(err)
	}
	key, err := s.auth().CreateAccessKey("agent", []string{"alpha"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	w := Wiki{Store: s, Token: "owner"}
	call := func(query, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/api/v1/articles/excerpt?"+query, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		out := httptest.NewRecorder()
		w.ServeHTTP(out, req)
		return out
	}
	first := call("source_id=alpha&path=guide.md&limit=2", key)
	var page struct {
		Text  string `json:"text"`
		SHA   string `json:"sha256"`
		Next  *int   `json:"next_offset"`
		Total int    `json:"total_characters"`
	}
	if first.Code != 200 || json.Unmarshal(first.Body.Bytes(), &page) != nil || page.Text != "я😀" || page.Next == nil || *page.Next != 2 || page.Total != len([]rune(original)) {
		t.Fatal("first slice invalid", first.Code)
	}
	next := call(fmt.Sprintf("source_id=alpha&path=guide.md&offset=2&limit=16000&expected_sha256=%s", page.SHA), key)
	var tail struct {
		Text string `json:"text"`
		Next *int   `json:"next_offset"`
	}
	if next.Code != 200 || json.Unmarshal(next.Body.Bytes(), &tail) != nil || tail.Text != "alpha\nфинал" || tail.Next != nil {
		t.Fatal("continuation invalid", next.Code)
	}
	if call("source_id=alpha&path=guide.md&offset=2", key).Code != 400 || call("source_id=alpha&path=guide.md&limit=16001", key).Code != 400 {
		t.Fatal("invalid continuation accepted")
	}
	if call("source_id=beta&path=guide.md", key).Code != 404 || call("source_id=alpha&path=guide.md", "").Code != 404 {
		t.Fatal("private scope escaped")
	}
	if _, err = s.put("alpha", "guide.md", "# Changed", 1); err != nil {
		t.Fatal(err)
	}
	if call(fmt.Sprintf("source_id=alpha&path=guide.md&offset=2&expected_sha256=%s", page.SHA), key).Code != 409 {
		t.Fatal("stale continuation accepted")
	}
}

func TestExcerptPublicUsesPublishedRevision(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "# Published"})
	connectFixture(t, s, f, false)
	s.mutate(t, func(db *domain.Database) {
		a := db.Articles["handbook/guide.md"]
		text := "# Private draft"
		a.Versions = append(a.Versions, domain.Revision{Number: 2, Text: text, SHA: domain.Digest([]byte(text))})
		db.Articles["handbook/guide.md"] = a
	})
	out := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/articles/excerpt?source_id=handbook&path=guide.md", nil)
	(Wiki{Store: s, Public: true}).ServeHTTP(out, req)
	if out.Code != 200 || !strings.Contains(out.Body.String(), "Published") || strings.Contains(out.Body.String(), "Private draft") {
		t.Fatal("public excerpt exposed a draft", out.Code)
	}
}
