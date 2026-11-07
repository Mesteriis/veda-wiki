package httpapi

import (
	"encoding/json"
	"testing"
	"time"
)

func TestChangesAndSourceCheck(t *testing.T) {
	w := Wiki{Store: testStore(t), Token: "test-owner"}
	for _, src := range []string{`{"id":"one","title":"One","kind":"wiki","visibility":"private"}`, `{"id":"pub","title":"Pub","kind":"wiki","visibility":"public"}`} {
		if r := request(w, "POST", "/api/v1/sources", src, true); r.Code != 201 {
			t.Fatal(r.Code, r.Body)
		}
	}
	before := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	for _, a := range []string{
		`{"source_id":"one","path":"README.md","text":"# One\n\n[gone](missing.md) [ok](guide.md)\n","expected_revision":0}`,
		`{"source_id":"one","path":"guide.md","text":"# Guide","expected_revision":0}`,
		`{"source_id":"pub","path":"README.md","text":"# Pub","expected_revision":0}`,
	} {
		if r := request(w, "POST", "/api/v1/articles", a, true); r.Code != 201 {
			t.Fatal(r.Code, r.Body)
		}
	}
	var changes struct {
		Results []struct {
			SourceID string `json:"source_id"`
			Path     string `json:"path"`
		} `json:"results"`
		Total int `json:"total"`
	}
	r := request(w, "GET", "/api/v1/changes?since="+before, "", true)
	if err := json.Unmarshal(r.Body.Bytes(), &changes); err != nil || r.Code != 200 || changes.Total != 3 {
		t.Fatal(r.Code, r.Body)
	}
	r = request(w, "GET", "/api/v1/changes?since="+before, "", false)
	if err := json.Unmarshal(r.Body.Bytes(), &changes); err != nil || changes.Total != 1 || changes.Results[0].SourceID != "pub" {
		t.Fatal("public changes must list only public pages", r.Body)
	}
	future := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	if r := request(w, "GET", "/api/v1/changes?since="+future+"&source_id=one", "", true); !json.Valid(r.Body.Bytes()) || r.Code != 200 {
		t.Fatal(r.Code)
	}
	if r := request(w, "GET", "/api/v1/changes?since=yesterday", "", true); r.Code != 400 {
		t.Fatal(r.Code)
	}

	var check struct {
		Articles int `json:"articles"`
		Total    int `json:"total"`
		Results  []struct {
			Path string `json:"path"`
			Code string `json:"code"`
			Line int    `json:"line"`
		} `json:"results"`
	}
	r = request(w, "GET", "/api/v1/sources/check?source_id=one", "", true)
	if err := json.Unmarshal(r.Body.Bytes(), &check); err != nil || r.Code != 200 || check.Articles != 2 || check.Total != 1 ||
		check.Results[0].Code != "link_missing" || check.Results[0].Path != "README.md" || check.Results[0].Line != 3 {
		t.Fatal(r.Code, r.Body)
	}
	if r := request(w, "GET", "/api/v1/sources/check?source_id=pub", "", false); r.Code != 404 {
		t.Fatal("anonymous callers must not run a source check", r.Code)
	}
	if r := request(w, "GET", "/api/v1/sources/check?source_id=absent", "", true); r.Code != 404 {
		t.Fatal(r.Code)
	}
}

func TestStaleListsUnverifiedAndOldArticles(t *testing.T) {
	w := Wiki{Store: testStore(t), Token: "test-owner"}
	if r := request(w, "POST", "/api/v1/sources", `{"id":"one","title":"One","kind":"wiki","visibility":"private"}`, true); r.Code != 201 {
		t.Fatal(r.Code, r.Body)
	}
	today := time.Now().UTC().Format("2006-01-02")
	for path, text := range map[string]string{"fresh.md": "---\nverified: " + today + "\n---\n# F", "old.md": "---\nverified: 2020-01-01\n---\n# O", "none.md": "# N"} {
		body, _ := json.Marshal(map[string]any{"source_id": "one", "path": path, "text": text, "expected_revision": 0})
		if r := request(w, "POST", "/api/v1/articles", string(body), true); r.Code != 201 {
			t.Fatal(r.Code, r.Body)
		}
	}
	var out struct {
		Results []struct {
			Path     string  `json:"path"`
			Verified *string `json:"verified"`
		} `json:"results"`
		Total      int `json:"total"`
		Unverified int `json:"unverified"`
	}
	r := request(w, "GET", "/api/v1/stale?days=30", "", true)
	if err := json.Unmarshal(r.Body.Bytes(), &out); err != nil || r.Code != 200 || out.Total != 2 || out.Unverified != 1 ||
		out.Results[0].Path != "old.md" || *out.Results[0].Verified != "2020-01-01" || out.Results[1].Path != "none.md" {
		t.Fatal(r.Code, r.Body)
	}
	if r := request(w, "GET", "/api/v1/stale", "", false); r.Code != 200 || !json.Valid(r.Body.Bytes()) {
		t.Fatal(r.Code)
	}
	for _, bad := range []string{"days=0", "days=x", "source_id=../x"} {
		if r := request(w, "GET", "/api/v1/stale?"+bad, "", true); r.Code != 400 {
			t.Fatal(bad, r.Code)
		}
	}
}
