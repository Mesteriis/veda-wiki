package httpapi

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// Only the KeepRevisions newest revisions are stored: the owner's history
// lists them, and expected_revision is compared with the newest number.
func TestOwnerHistoryListsStoredRevisions(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	w := Wiki{Store: s, Token: "test-owner"}
	for n := range 5 {
		method := "PUT"
		if n == 0 {
			method = "POST"
		}
		body := `{"source_id":"local","path":"guide.md","text":"# Revision ` + strconv.Itoa(n+1) + `","expected_revision":` + strconv.Itoa(n) + `}`
		if r := request(w, method, "/api/v1/articles", body, true); r.Code != 200 && r.Code != 201 {
			t.Fatal(method, r.Code, r.Body)
		}
	}
	history := func() []int {
		r := request(w, "GET", "/api/v1/articles?source_id=local&path=guide.md&history=1", "", true)
		var a domain.Article
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &a) != nil {
			t.Fatal("owner history", r.Code)
		}
		var numbers []int
		for _, v := range a.Versions {
			numbers = append(numbers, v.Number)
		}
		return numbers
	}
	if got := history(); len(got) != 3 || got[0] != 3 || got[2] != 5 {
		t.Fatal("owner history", got)
	}
	stale := `{"source_id":"local","path":"guide.md","text":"# Stale","expected_revision":3}`
	if r := request(w, "PUT", "/api/v1/articles", stale, true); r.Code != 409 {
		t.Fatal("stale revision accepted", r.Code)
	}
	current := `{"source_id":"local","path":"guide.md","text":"# Revision 6","expected_revision":5}`
	if r := request(w, "PUT", "/api/v1/articles", current, true); r.Code != 200 {
		t.Fatal("current revision rejected", r.Code, r.Body)
	}
	if got := history(); len(got) != 3 || got[0] != 4 || got[2] != 6 {
		t.Fatal("owner history after write", got)
	}
}
