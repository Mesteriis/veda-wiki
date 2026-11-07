package httpapi

import (
	"encoding/json"
	"testing"
)

func TestArticleEditReplacesOneFragment(t *testing.T) {
	w := Wiki{Store: testStore(t), Token: "test-owner"}
	if r := request(w, "POST", "/api/v1/sources", `{"id":"one","title":"One","kind":"wiki","visibility":"private"}`, true); r.Code != 201 {
		t.Fatal(r.Code, r.Body)
	}
	if r := request(w, "POST", "/api/v1/articles", `{"source_id":"one","path":"a.md","text":"# A\n\nalpha twice twice\n","expected_revision":0}`, true); r.Code != 201 {
		t.Fatal(r.Code, r.Body)
	}
	edit := func(old, replacement string, expected int) (int, map[string]json.RawMessage) {
		body, _ := json.Marshal(map[string]any{"source_id": "one", "path": "a.md", "old_text": old, "new_text": replacement, "expected_revision": expected})
		r := request(w, "PATCH", "/api/v1/articles", string(body), true)
		var out map[string]json.RawMessage
		_ = json.Unmarshal(r.Body.Bytes(), &out)
		return r.Code, out
	}
	code, out := edit("alpha", "<div>beta</div>", 1)
	if code != 200 || string(out["revision"]) != "2" || out["sha256"] == nil {
		t.Fatal(code, out)
	}
	if _, echoed := out["text"]; echoed {
		t.Fatal("edit response must not echo the article text")
	}
	if out["warnings"] == nil {
		t.Fatal("edit response must carry formatting warnings of the new text", out)
	}
	for _, c := range []struct {
		old      string
		expected int
		code     int
		error    string
	}{
		{"alpha", 2, 422, `"fragment_not_found"`},
		{"twice", 2, 422, `"fragment_ambiguous"`},
		{"beta", 1, 409, ""},
		{"", 2, 400, ""},
	} {
		code, out := edit(c.old, "x", c.expected)
		if code != c.code || (c.error != "" && string(out["error"]) != c.error) {
			t.Fatal(c, code, out)
		}
	}
	r := request(w, "GET", "/api/v1/articles?source_id=one&path=a.md", "", true)
	var article struct {
		Versions []struct {
			Text string `json:"text"`
		} `json:"versions"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &article); err != nil || len(article.Versions) != 1 ||
		article.Versions[0].Text != "# A\n\n<div>beta</div> twice twice\n" {
		t.Fatal(r.Body)
	}
}
