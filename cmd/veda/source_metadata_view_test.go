package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSourceMetadataViewRespectsVisibilityAndScopes(t *testing.T) {
	s := testStore(t)
	for _, id := range []string{"alpha", "beta"} {
		b := testBatch()
		b.SourceID = id
		b.Metadata.Title = "Title " + id
		b.Metadata.Description = "Description " + id
		b.Metadata.URL = "https://example.invalid/" + id
		b.Metadata.Image = "assets/cover.png"
		b.Assets = map[string][]byte{"assets/cover.png": smallPNG(t)}
		if _, err := s.importBatch(b); err != nil {
			t.Fatal(err)
		}
	}
	key, err := s.auth().CreateAccessKey("scoped", []string{"alpha"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	w := Wiki{Store: s, Token: "owner"}
	req := httptest.NewRequest("GET", "/api/state", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	out := httptest.NewRecorder()
	w.ServeHTTP(out, req)
	var payload struct {
		View View `json:"view"`
	}
	if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &payload) != nil {
		t.Fatal("scoped state failed", out.Code)
	}
	if len(payload.View.Groups) != 1 || payload.View.Groups[0].ID != "alpha" || payload.View.Groups[0].Description != "Description alpha" || payload.View.Groups[0].SourceURL != "https://example.invalid/alpha" {
		t.Fatal("metadata missing or wrong")
	}
	if len(payload.View.Cards) != 1 || payload.View.Cards[0].ImageURL != "/media/alpha/assets%2Fcover.png" {
		t.Fatal("source cover missing")
	}
	if strings.Contains(out.Body.String(), "beta") {
		t.Fatal("metadata escaped scoped source")
	}
	public := request(Wiki{Store: s, Public: true}, "GET", "/api/state", "", false)
	if public.Code != 200 || strings.Contains(public.Body.String(), "example.invalid") {
		t.Fatal("private metadata exposed publicly")
	}
}
