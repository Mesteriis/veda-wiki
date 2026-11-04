package httpapi

import (
	"encoding/json"
	"github.com/Mesteriis/veda-wiki/internal/domain"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLargeArticlesPersistWithVersionGateAndAPI(t *testing.T) {
	s := testStore(t)
	source(t, s, "fixture", "private")
	text := "# Large\n" + strings.Repeat("word ", 140000)
	payload, _ := json.Marshal(map[string]any{"source_id": "fixture", "path": "large.md", "text": text, "expected_revision": 0})
	w := Wiki{Store: s, Token: "test-owner"}
	req := httptest.NewRequest("POST", "/api/v1/articles", strings.NewReader(string(payload)))
	req.Header.Set("Authorization", "Bearer test-owner")
	out := httptest.NewRecorder()
	w.ServeHTTP(out, req)
	if out.Code != 201 || s.state(t).Version != domain.RevisionsFormat {
		t.Fatal("large article not written at the current store format", out.Code, s.state(t).Version)
	}
	reopened, err := openStore(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	a, err := reopened.article("fixture", "large.md", false)
	if err != nil || a.Versions[0].Text != text {
		t.Fatal("large article did not survive restart", err)
	}
	if _, err = reopened.put("fixture", "too-large.md", strings.Repeat("x", domain.MaxArticleBytes+1), 0); err != domain.ErrInvalid {
		t.Fatal("oversize write accepted", err)
	}
}
