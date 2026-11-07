package httpapi

import (
	"encoding/json"
	"testing"
)

func TestOutlineListsSectionsOfTheVisibleRevision(t *testing.T) {
	w := Wiki{Store: testStore(t), Token: "test-owner"}
	if r := request(w, "POST", "/api/v1/sources", `{"id":"one","title":"One","kind":"wiki","visibility":"private"}`, true); r.Code != 201 {
		t.Fatal(r.Code, r.Body)
	}
	if r := request(w, "POST", "/api/v1/articles", `{"source_id":"one","path":"a.md","text":"# A\n\n## B\ntext\n","expected_revision":0}`, true); r.Code != 201 {
		t.Fatal(r.Code, r.Body)
	}
	r := request(w, "GET", "/api/v1/articles/outline?source_id=one&path=a.md", "", true)
	var out struct {
		Revision int    `json:"revision"`
		SHA      string `json:"sha256"`
		Total    int    `json:"total_characters"`
		Sections []struct {
			Level      int    `json:"level"`
			Title      string `json:"title"`
			Offset     int    `json:"offset"`
			Characters int    `json:"characters"`
		} `json:"sections"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &out); err != nil || r.Code != 200 || out.Revision != 1 || len(out.SHA) != 64 ||
		out.Total != 15 || len(out.Sections) != 2 || out.Sections[1].Title != "B" || out.Sections[1].Offset != 5 || out.Sections[1].Characters != 10 {
		t.Fatal(r.Code, r.Body)
	}
	if r := request(w, "GET", "/api/v1/articles/outline?source_id=one&path=a.md", "", false); r.Code != 404 {
		t.Fatal("a private article must stay hidden from the public", r.Code)
	}
	if r := request(w, "GET", "/api/v1/articles/outline?source_id=one", "", true); r.Code != 400 {
		t.Fatal(r.Code)
	}
	if r := request(w, "POST", "/api/v1/articles/outline?source_id=one&path=a.md", "", true); r.Code != 405 {
		t.Fatal(r.Code)
	}
}
