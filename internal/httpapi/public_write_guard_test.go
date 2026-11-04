package httpapi

import (
	"encoding/json"
	"errors"
	"github.com/Mesteriis/veda-wiki/internal/domain"
	"net/http"
	"strings"
	"testing"
)

const credentialFixture = "password=synthetic-fixture-only"

func TestPublicWriteRejectsCredentialLikeTextWithoutChangingHistory(t *testing.T) {
	s := testStore(t)
	source(t, s, "open", "public")
	source(t, s, "closed", "private")
	if _, err := s.put("open", "guide.md", "# Safe", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("open", "guide.md", credentialFixture, 1); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal("public update accepted", err)
	}
	if _, err := s.put("open", "new.md", credentialFixture, 0); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal("public creation accepted", err)
	}
	if _, err := s.put("closed", "guide.md", credentialFixture, 0); err != nil {
		t.Fatal("private write rejected", err)
	}
	a, err := s.article("open", "guide.md", false)
	if err != nil || len(a.Versions) != 1 || a.Versions[0].Text != "# Safe" {
		t.Fatal("failed public write changed history", err)
	}
	if _, err = s.article("open", "new.md", false); err != domain.ErrNotFound {
		t.Fatal("failed public creation left article", err)
	}
	body, err := json.Marshal(map[string]any{"source_id": "open", "path": "api.md", "text": credentialFixture, "expected_revision": 0})
	if err != nil {
		t.Fatal(err)
	}
	response := request(Wiki{Store: s, Token: "test-owner"}, http.MethodPost, "/api/v1/articles", string(body), true)
	if response.Code != http.StatusBadRequest || strings.Contains(response.Body.String(), credentialFixture) {
		t.Fatal("unsafe REST write accepted or echoed", response.Code)
	}
}
