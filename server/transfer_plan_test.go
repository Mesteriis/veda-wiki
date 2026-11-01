package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTransferPlanCyclesAndDestinationStates(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	source(t, s, "to", "private")
	s.put("from", "a.md", "[B](b.md) [C](c.md)", 0)
	s.put("from", "b.md", "[A](a.md)", 0)
	s.put("from", "c.md", "# C", 0)
	s.put("to", "b.md", "[A](a.md)", 0)
	s.put("to", "c.md", "# different", 0)
	before, _ := json.Marshal(s.db)
	p, err := s.planTransfer(copyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "a.md"})
	if err != nil || len(p.Articles) != 3 {
		t.Fatal(p, err)
	}
	if p.Articles[0].Destination != "missing" || p.Articles[1].Destination != "matching" || p.Articles[2].Destination != "conflict" {
		t.Fatal(p)
	}
	after, _ := json.Marshal(s.db)
	if string(before) != string(after) {
		t.Fatal("plan changed state")
	}
}
func TestTransferPlanRejectsMissingAndAmbiguousPaths(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	source(t, s, "to", "private")
	s.put("from", "a.md", "[B](b.md)", 0)
	req := copyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "renamed.md"}
	if _, err := s.planTransfer(req); err != ErrNotFound {
		t.Fatal(err)
	}
	s.put("from", "b.md", "[A](a.md)", 0)
	if _, err := s.planTransfer(req); err != ErrConflict {
		t.Fatal(err)
	}
	req.ExpectedRevision = 2
	if _, err := s.planTransfer(req); err != ErrConflict {
		t.Fatal(err)
	}
}

func TestTransferPlanOwnerOnly(t *testing.T) {
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
	for _, tc := range []struct{ token, origin string }{{"read-only", ""}, {"test-owner", "http://localhost"}} {
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
