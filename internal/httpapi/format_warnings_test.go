package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/render"
)

func TestArticleWriteReturnsFormatWarnings(t *testing.T) {
	w := Wiki{Store: testStore(t), Token: "test-owner"}
	if r := request(w, "POST", "/api/v1/sources", `{"id":"one","title":"One","kind":"wiki","visibility":"private"}`, true); r.Code != 201 {
		t.Fatal(r.Code, r.Body)
	}
	decode := func(body string) map[string]json.RawMessage {
		var out map[string]json.RawMessage
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	r := request(w, "POST", "/api/v1/articles", `{"source_id":"one","path":"a.md","text":"# A\n\n<div>x</div>","expected_revision":0}`, true)
	if r.Code != 201 {
		t.Fatal(r.Code, r.Body)
	}
	var warnings []render.Warning
	if err := json.Unmarshal(decode(r.Body.String())["warnings"], &warnings); err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || warnings[0] != (render.Warning{Code: "raw_html", Line: 3, Message: "<div> is not allowed and is removed"}) {
		t.Fatalf("response %s", r.Body)
	}
	r = request(w, "PUT", "/api/v1/articles", `{"source_id":"one","path":"a.md","text":"# A\n\nClean.","expected_revision":1}`, true)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body)
	}
	if _, present := decode(r.Body.String())["warnings"]; present {
		t.Fatalf("clean write has warnings: %s", r.Body)
	}
}
