package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Mesteriis/veda-wiki/internal/domain"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestPreviousBinaryRefusesLargeArticleStore(t *testing.T) {
	old := os.Getenv("VEDA_OLD_V12_BINARY")
	if old == "" {
		t.Skip("old binary not provided")
	}
	s := testStore(t)
	source(t, s, "fixture", "private")
	if _, err := s.put("fixture", "large.md", strings.Repeat("x", domain.LegacyArticleBytes+1), 0); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(s.Dir(), "state.json")
	before, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, old, "--data", s.Dir(), "--listen", "127.0.0.1:0", "--public-listen", "127.0.0.1:0").CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || ctx.Err() != nil || !strings.Contains(string(output), "unsupported store") {
		t.Fatal("old binary did not reject the store")
	}
	after, err := os.ReadFile(state)
	if err != nil || string(before) != string(after) {
		t.Fatal("old binary changed the store")
	}
}
