package httpapi

import (
	"context"
	"encoding/json"
	"github.com/Mesteriis/veda-wiki/internal/domain"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
)

func TestGitHubObservationTracksImportedOrVerifiedHead(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "# Initial"})
	m := connectFixture(t, s, f, false)
	ctx, err := s.agentContext("handbook", "guide.md", false)
	if err != nil || ctx.SourceStatus.LastImportObservedAt == nil {
		t.Fatal("successful GitHub import not observed", err)
	}
	public, err := s.agentContext("handbook", "guide.md", true)
	if err != nil || public.SourceStatus.LastImportObservedAt != nil || s.sources(true)[0].LastImportObservedAt != "" {
		t.Fatal("public context exposed observation metadata", err)
	}
	old := time.Now().Add(-time.Hour).UTC()
	s.observations.MarkObserved("handbook", old)
	m.Once(context.Background())
	observed := s.sources(false)[0].LastImportObservedAt
	stamp, err := time.Parse(time.RFC3339Nano, observed)
	if err != nil || !stamp.After(old) {
		t.Fatal("unchanged verified head not observed", err)
	}
	f.Modify(func() { f.Fail = true })
	syncFixtureSource(t, m, "handbook")
	if s.sources(false)[0].LastImportObservedAt != observed {
		t.Fatal("failed GitHub check advanced observation")
	}
	// internal/syncer TestStaleConnectionCannotConfirmObservedHead covers a
	// stale confirmation of the unchanged head.
	reopened, err := openStore(s.Dir())
	if err != nil || reopened.sources(false)[0].LastImportObservedAt != "" {
		t.Fatal("observation persisted across restart", err)
	}
}

func TestReaderWarningsReportOnlyVisibleGitHubFailures(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "# Local copy"})
	m := connectFixture(t, s, f, false)
	source(t, s, "other", "private")
	key, err := s.auth().CreateAccessKey("agent", []string{"handbook"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	read := func(token string, public bool) []SourceWarning {
		t.Helper()
		r := httptest.NewRequest("GET", "/api/state", nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		out := httptest.NewRecorder()
		(Wiki{Store: s, Token: "owner", Public: public}).ServeHTTP(out, r)
		var response struct {
			View View `json:"view"`
		}
		if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &response) != nil {
			t.Fatal("state unavailable", out.Code)
		}
		return response.View.SourceWarnings
	}
	if got := read(key, false); len(got) != 0 {
		t.Fatal("healthy source warned", got)
	}
	f.Modify(func() { f.Fail = true })
	m.Once(context.Background())
	if got := read(key, false); len(got) != 1 || got[0].ID != "handbook" || got[0].SyncIssue != "error" || got[0].LastImportObservedAt == "" {
		t.Fatal("scoped error missing", got)
	}
	if got := read("", true); len(got) != 0 {
		t.Fatal("public state exposed private status", got)
	}
	if page, err := s.page("handbook/guide.md", false); err != nil || page.Text != "# Local copy" {
		t.Fatal("outage removed local copy", err)
	}
	f.Modify(func() { f.Fail = false })
	if err = s.retryConnection("handbook"); err != nil {
		t.Fatal(err)
	}
	m.Once(context.Background())
	if got := read(key, false); len(got) != 0 {
		t.Fatal("warning remained after recovery", got)
	}
	if err = s.pauseConnection("handbook", true); err != nil {
		t.Fatal(err)
	}
	if got := read(key, false); len(got) != 1 || got[0].SyncIssue != "paused" {
		t.Fatal("pause not reported", got)
	}
}

func TestImportObservationIsTransientAndOnlyAdvancesOnSuccess(t *testing.T) {
	s := testStore(t)
	batch := testBatch()
	batch.Visibility = "public"
	if _, e := s.importBatch(batch); e != nil {
		t.Fatal(e)
	}
	source := s.sources(false)[0]
	if source.LastImportObservedAt == "" {
		t.Fatal("successful import not observed")
	}
	if public := s.sources(true)[0]; public.LastImportObservedAt != "" || public.UnresolvedConflicts != 0 {
		t.Fatal("public observation metadata exposed")
	}
	old := time.Now().Add(-time.Hour).UTC()
	s.observations.MarkObserved("handbook", old)
	batch.Create = false
	gen := s.state(t).Sources["handbook"].ImportGeneration
	batch.ExpectedImport = &gen
	if _, e := s.importBatch(batch); e != nil {
		t.Fatal(e)
	}
	observed := s.sources(false)[0].LastImportObservedAt
	parsed, e := time.Parse(time.RFC3339Nano, observed)
	if e != nil || !parsed.After(old) {
		t.Fatal("unchanged import did not refresh observation", e)
	}
	stale := "not-the-current-generation"
	batch.ExpectedImport = &stale
	if _, e := s.importBatch(batch); e != domain.ErrConflict {
		t.Fatal("stale generation accepted", e)
	}
	if s.sources(false)[0].LastImportObservedAt != observed {
		t.Fatal("failed import advanced observation")
	}
	reopened, e := openStore(s.Dir())
	if e != nil {
		t.Fatal(e)
	}
	if reopened.sources(false)[0].LastImportObservedAt != "" {
		t.Fatal("observation should be unknown after restart")
	}
}
func TestContextSourceStatusCountsUnresolvedConflicts(t *testing.T) {
	s := testStore(t)
	batch := testBatch()
	if _, e := s.importBatch(batch); e != nil {
		t.Fatal(e)
	}
	if _, e := s.put("handbook", "README.md", "# Local", 1); e != nil {
		t.Fatal(e)
	}
	batch = applyAgain(s, batch)
	batch.Files["README.md"] = "# Upstream"
	if _, e := s.importBatch(batch); e != nil {
		t.Fatal(e)
	}
	view := s.sources(false)[0]
	if view.UnresolvedConflicts != 1 {
		t.Fatal("missing unresolved conflict count")
	}
	w := Wiki{Store: s, Token: "test-owner"}
	r := request(w, "GET", "/api/v1/sources", "", true)
	var response []map[string]any
	if e := json.Unmarshal(r.Body.Bytes(), &response); e != nil || len(response) != 1 || response[0]["unresolved_conflicts"] != float64(1) || !strings.Contains(r.Body.String(), "last_import_observed_at") {
		t.Fatal("source status response", e)
	}
}

func TestStateShowsWarningsOnlyForVisiblePrivateSources(t *testing.T) {
	s := testStore(t)
	source(t, s, "alpha", "private")
	source(t, s, "beta", "private")
	source(t, s, "open", "public")
	for _, id := range []string{"alpha", "beta", "open"} {
		if _, err := s.put(id, "a.md", "# Synthetic", 0); err != nil {
			t.Fatal(err)
		}
	}
	s.mutate(t, func(db *domain.Database) {
		for _, id := range []string{"alpha", "beta", "open"} {
			src := db.Sources[id]
			src.ImportPartial = true
			db.Sources[id] = src
		}
	})
	key, err := s.auth().CreateAccessKey("reader", []string{"alpha"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	w := Wiki{Store: s, Token: "owner"}
	read := func(token string) []SourceWarning {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/state", nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		out := httptest.NewRecorder()
		w.ServeHTTP(out, req)
		var payload struct {
			View View `json:"view"`
		}
		if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &payload) != nil {
			t.Fatal("state failed", out.Code)
		}
		return payload.View.SourceWarnings
	}
	if got := read(""); len(got) != 0 {
		t.Fatal("public state exposed warnings")
	}
	if got := read(key); len(got) != 1 || got[0].ID != "alpha" || !got[0].Partial || got[0].LastImportObservedAt != "" {
		t.Fatal("scoped warnings were missing or leaked")
	}
	if got := read("owner"); len(got) != 3 {
		t.Fatal("owner did not receive all visible warnings")
	}
}
