package httpapi

import (
	"context"
	"encoding/json"
	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
	"github.com/Mesteriis/veda-wiki/internal/store"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProposalReportsPreparedWriteOnlyForSelectedArticle(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "a.md": "# First", "b.md": "# Second", "linked.md": "# Linked", "dependency.md": "# Dependency"})
	m := connectFixture(t, s, f, true)
	if _, err := s.put("handbook", "a.md", "# Local", 1); err != nil {
		t.Fatal(err)
	}
	f.Modify(func() { f.LoseResponse = true })
	m.Once(context.Background())
	key, err := s.auth().CreateAccessKey("agent", []string{"handbook"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path    string
		blocked bool
	}{{"a.md", true}, {"b.md", false}} {
		r := httptest.NewRequest("GET", "/api/v1/articles/proposal-base?source_id=handbook&path="+tc.path, nil)
		r.Header.Set("Authorization", "Bearer "+key)
		out := httptest.NewRecorder()
		(Wiki{Store: s, Token: "owner"}).ServeHTTP(out, r)
		var body map[string]any
		if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &body) != nil || body["conflict_resolution_blocked_by_write"] != tc.blocked || body["write_blocked_by_bundle"] != false {
			t.Fatal("incorrect article write status", tc.path, out.Code)
		}
		if strings.Contains(out.Body.String(), "prepared_commit") {
			t.Fatal("commit identifier exposed")
		}
	}
	// A linked bundle blocks resolution even before a ref update is prepared.
	s.mutate(t, func(db *domain.Database) {
		db.Outbox["fixture-bundle"] = domain.SyncJob{SourceID: "handbook", Path: "b.md", State: "pending", Bundle: map[string]string{"linked.md": strings.Repeat("a", 64)}, Links: map[string]string{"dependency.md": strings.Repeat("b", 64)}}
	})
	for _, path := range []string{"b.md", "linked.md", "dependency.md"} {
		r := httptest.NewRequest("GET", "/api/v1/articles/proposal-base?source_id=handbook&path="+path, nil)
		r.Header.Set("Authorization", "Bearer "+key)
		out := httptest.NewRecorder()
		(Wiki{Store: s, Token: "owner"}).ServeHTTP(out, r)
		var body map[string]any
		if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &body) != nil || body["conflict_resolution_blocked_by_write"] != true || body["write_blocked_by_bundle"] != true || strings.Contains(out.Body.String(), "linked.md") || strings.Contains(out.Body.String(), "dependency.md") {
			t.Fatal("linked bundle status missing or leaked", path, out.Code)
		}
	}
}

func TestProposalBaseAndWriteDetectConflictWithoutRevisionChange(t *testing.T) {
	for _, sqlite := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "sqlite"}[sqlite], func(t *testing.T) {
			testProposalConflict(t, sqlite)
		})
	}
}

func testProposalConflict(t *testing.T, sqlite bool) {
	t.Helper()
	s := testStore(t)
	batch := testBatch()
	if _, err := s.importBatch(batch); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("handbook", "README.md", "# Local", 1); err != nil {
		t.Fatal(err)
	}
	if sqlite {
		if err := store.MigrateSQLite(s.Dir()); err != nil {
			t.Fatal(err)
		}
		var err error
		s, err = openStore(s.Dir())
		if err != nil {
			t.Fatal(err)
		}
	}
	source(t, s, "other", "private")
	if _, err := s.put("other", "hidden.md", "# Hidden", 0); err != nil {
		t.Fatal(err)
	}
	key, err := s.auth().CreateAccessKey("agent", []string{"handbook"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	read := func(token string, public bool) (*httptest.ResponseRecorder, map[string]any) {
		req := httptest.NewRequest("GET", "/api/v1/articles/proposal-base?source_id=handbook&path=README.md", nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		out := httptest.NewRecorder()
		(Wiki{Store: s, Token: "owner", Public: public}).ServeHTTP(out, req)
		var body map[string]any
		if out.Code == 200 && json.Unmarshal(out.Body.Bytes(), &body) != nil {
			t.Fatal("invalid response")
		}
		return out, body
	}
	response, before := read(key, false)
	if response.Code != 200 || before["revision"] != float64(2) || before["unresolved_conflict"] != false {
		t.Fatal("initial proposal base unavailable", response.Code)
	}
	initialStatus, ok := before["source_status"].(map[string]any)
	if !ok || initialStatus["unresolved_conflicts"] != float64(0) {
		t.Fatal("initial source status missing")
	}
	batch = applyAgain(s, batch)
	batch.Files["README.md"] = "# Remote"
	result, err := s.importBatch(batch)
	if err != nil || result.Conflicts != 1 {
		t.Fatal("fixture conflict not created", err)
	}
	response, after := read(key, false)
	if response.Code != 200 || after["revision"] != before["revision"] || after["sha256"] != before["sha256"] || after["unresolved_conflict"] != true {
		t.Fatal("conflict status missing", response.Code)
	}
	conflictStatus, ok := after["source_status"].(map[string]any)
	if !ok || conflictStatus["unresolved_conflicts"] != float64(1) || conflictStatus["last_import_observed_at"] == nil {
		t.Fatal("proposal source status did not reflect import")
	}
	if _, err = s.put("handbook", "README.md", "# Proposed", 2); err != domain.ErrConflict {
		t.Fatal("write ignored unresolved conflict", err)
	}
	if response, _ = read("", true); response.Code != 404 {
		t.Fatal("public conflict status exposed", response.Code)
	}
	if response, _ = read("owner", true); response.Code != 404 {
		t.Fatal("public listener accepted owner credentials", response.Code)
	}
	request := httptest.NewRequest("GET", "/api/v1/articles/proposal-base?source_id=other&path=hidden.md", nil)
	request.Header.Set("Authorization", "Bearer "+key)
	denied := httptest.NewRecorder()
	(Wiki{Store: s, Token: "owner"}).ServeHTTP(denied, request)
	if denied.Code != 404 {
		t.Fatal("proposal base escaped scope", denied.Code)
	}
	if err = s.resolveConflict("handbook", "README.md", 0, 2, "local"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.put("handbook", "README.md", "# Reviewed proposal", 2); err != nil {
		t.Fatal("write after resolution failed", err)
	}
	reopened, err := openStore(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	a, err := reopened.article("handbook", "README.md", false)
	if err != nil || len(a.Versions) != 3 || a.Versions[2].Text != "# Reviewed proposal" {
		t.Fatal("resolved write not durable", err)
	}
}

func TestNewProposalBaseChecksScopeAndVisiblePath(t *testing.T) {
	s := testStore(t)
	source(t, s, "alpha", "private")
	source(t, s, "other", "private")
	if _, err := s.put("alpha", "exists.md", "# Existing", 0); err != nil {
		t.Fatal(err)
	}
	key, err := s.auth().CreateAccessKey("agent", []string{"alpha"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	call := func(route, token string, public bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/api/v1/articles/new-proposal-base?"+route, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res := httptest.NewRecorder()
		(Wiki{Store: s, Token: "owner", Public: public}).ServeHTTP(res, req)
		return res
	}
	result := call("source_id=alpha&path=new.md", key, false)
	var payload struct {
		SourceStatus domain.SourceStatus `json:"source_status"`
	}
	if result.Code != 200 || json.Unmarshal(result.Body.Bytes(), &payload) != nil || payload.SourceStatus.LastImportObservedAt != nil {
		t.Fatal("new proposal status unavailable", result.Code)
	}
	if call("source_id=alpha&path=exists.md", key, false).Code != 409 {
		t.Fatal("existing article accepted")
	}
	if call("source_id=other&path=new.md", key, false).Code != 404 {
		t.Fatal("scope bypass")
	}
	if call("source_id=alpha&path=new.md", "owner", true).Code != 404 {
		t.Fatal("public listener exposed proposal")
	}
	if call("source_id=alpha&path=..%2Fbad.md", key, false).Code != 400 {
		t.Fatal("invalid path accepted")
	}
	s.mutate(t, func(db *domain.Database) {
		a := db.Articles["alpha/exists.md"]
		a.Deleted = true
		db.Articles["alpha/exists.md"] = a
	})
	if call("source_id=alpha&path=exists.md", key, false).Code != 200 {
		t.Fatal("deleted history disclosed")
	}
	if _, err := s.put("alpha", "exists.md", "# New", 0); err != domain.ErrConflict {
		t.Fatal("tombstoned history overwritten")
	}
}
