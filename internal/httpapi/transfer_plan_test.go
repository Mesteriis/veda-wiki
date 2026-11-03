package httpapi

import (
	"encoding/json"
	"github.com/Mesteriis/veda-wiki/internal/domain"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTransferPlanReaderAccessAndBrowserBoundary(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	source(t, s, "to", "private")
	s.put("from", "a.md", "# A", 0)
	w := Wiki{Store: s, Token: "test-owner", ReaderToken: "read-only"}
	body := `{"source_id":"from","source_path":"a.md","expected_revision":1,"destination_id":"to","destination_path":"a.md"}`
	if r := request(w, "POST", "/api/v1/articles/transfer-plan", body, false); r.Code != 403 {
		t.Fatal("anonymous plan accepted", r.Code)
	}
	if r := request(w, "POST", "/api/v1/articles/transfer-plan", body, true); r.Code != 200 {
		t.Fatal("owner plan rejected", r.Code)
	}
	readerReq := httptest.NewRequest("POST", "/api/v1/articles/transfer-plan", strings.NewReader(body))
	readerReq.Header.Set("Authorization", "Bearer read-only")
	readerResponse := httptest.NewRecorder()
	w.ServeHTTP(readerResponse, readerReq)
	if readerResponse.Code != 200 {
		t.Fatal("reader plan rejected", readerResponse.Code)
	}
	public := w
	public.Public = true
	publicResponse := httptest.NewRecorder()
	public.ServeHTTP(publicResponse, readerReq)
	if publicResponse.Code != 403 {
		t.Fatal("public listener accepted transfer plan", publicResponse.Code)
	}
	for _, tc := range []struct{ token, origin string }{{"read-only", "http://localhost"}, {"test-owner", "http://localhost"}} {
		r := httptest.NewRequest("POST", "/api/v1/articles/transfer-plan", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+tc.token)
		if tc.origin != "" {
			r.Header.Set("Origin", tc.origin)
		}
		response := httptest.NewRecorder()
		w.ServeHTTP(response, r)
		if response.Code != 403 {
			t.Fatal("unauthorized plan accepted", response.Code)
		}
	}
}

func TestTransferPlanScopedReadersNeedBothSources(t *testing.T) {
	s := testStore(t)
	for _, id := range []string{"from", "to", "hidden"} {
		source(t, s, id, "private")
	}
	if _, err := s.put("from", "a.md", "# A", 0); err != nil {
		t.Fatal(err)
	}
	key, err := s.auth().CreateAccessKey("scoped-plan", []string{"from", "to"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	w := Wiki{Store: s, Token: "test-owner"}
	call := func(path, token string, body map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest("POST", path, strings.NewReader(string(raw)))
		req.Header.Set("Authorization", "Bearer "+token)
		out := httptest.NewRecorder()
		w.ServeHTTP(out, req)
		return out
	}
	plan := map[string]any{"source_id": "from", "source_path": "a.md", "expected_revision": 1, "destination_id": "to", "destination_path": "a.md"}
	before, _ := json.Marshal(s.state(t))
	if got := call("/api/v1/articles/transfer-plan", key, plan); got.Code != 200 || strings.Contains(got.Body.String(), "# A") {
		t.Fatal("scoped plan failed or returned article text", got.Code)
	}
	plan["destination_id"] = "hidden"
	if got := call("/api/v1/articles/transfer-plan", key, plan); got.Code != 404 {
		t.Fatal("hidden destination visible", got.Code)
	}
	plan["source_id"], plan["destination_id"] = "hidden", "to"
	if got := call("/api/v1/articles/transfer-plan", key, plan); got.Code != 404 {
		t.Fatal("hidden source visible", got.Code)
	}
	plan["source_id"], plan["destination_id"] = "from", "to"
	if got := call("/api/v1/articles/copy", key, plan); got.Code != 403 {
		t.Fatal("scoped reader copied article", got.Code)
	}
	after, _ := json.Marshal(s.state(t))
	if string(before) != string(after) {
		t.Fatal("read-only plan changed state")
	}
	if err = s.auth().RevokeAccessKey("scoped-plan"); err != nil {
		t.Fatal(err)
	}
	if got := call("/api/v1/articles/transfer-plan", key, plan); got.Code == 200 {
		t.Fatal("revoked reader accessed plan")
	}
}

func TestTransferPlanMarksCredentialLikeRevisionsWithoutEchoingContent(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	source(t, s, "to", "public")
	if _, err := s.put("from", "root.md", "# Root\n\n[Child](child.md)", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("from", "child.md", credentialFixture, 0); err != nil {
		t.Fatal(err)
	}
	body := `{"source_id":"from","source_path":"root.md","expected_revision":1,"destination_id":"to","destination_path":"root.md"}`
	response := request(Wiki{Store: s, Token: "test-owner"}, "POST", "/api/v1/articles/transfer-plan", body, true)
	var plan transferPlan
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &plan) != nil || len(plan.Articles) != 2 {
		t.Fatal("transfer plan unavailable", response.Code)
	}
	seen := map[string]bool{}
	for _, item := range plan.Articles {
		seen[item.SourcePath] = item.CredentialLike
	}
	if seen["root.md"] || !seen["child.md"] || strings.Contains(response.Body.String(), credentialFixture) {
		t.Fatal("incorrect credential-like annotations or content echoed")
	}
}

func TestTransferPlanIncludesBothAuthorizedSourceStatuses(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	source(t, s, "to", "private")
	if _, err := s.put("from", "root.md", "# Root", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("from", "other.md", "# Other", 0); err != nil {
		t.Fatal(err)
	}
	s.mutate(t, func(db *domain.Database) {
		from := db.Sources["from"]
		from.ImportDirty = true
		db.Sources["from"] = from
		to := db.Sources["to"]
		to.ImportPartial = true
		db.Sources["to"] = to
		a := db.Articles["from/other.md"]
		a.Conflicts = []domain.ImportConflict{{}}
		db.Articles["from/other.md"] = a
	})
	key, err := s.auth().CreateAccessKey("plan-status", []string{"from", "to"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	body := `{"source_id":"from","source_path":"root.md","expected_revision":1,"destination_id":"to","destination_path":"root.md"}`
	req := httptest.NewRequest("POST", "/api/v1/articles/transfer-plan", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	out := httptest.NewRecorder()
	(Wiki{Store: s, Token: "test-owner"}).ServeHTTP(out, req)
	var plan transferPlan
	if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &plan) != nil {
		t.Fatal("plan unavailable", out.Code)
	}
	if !plan.SourceStatus.ImportDirty || plan.SourceStatus.UnresolvedConflicts != 1 || plan.SourceStatus.LastImportObservedAt != nil || !plan.DestinationStatus.ImportPartial || plan.DestinationStatus.LastImportObservedAt != nil {
		t.Fatal("incomplete or incorrect source status", plan.SourceStatus, plan.DestinationStatus)
	}
}
