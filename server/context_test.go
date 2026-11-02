package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestAgentContextScopesAndReportsStatus(t *testing.T) {
	s := testStore(t)
	for _, id := range []string{"alpha", "beta"} {
		source(t, s, id, "private")
		for _, name := range []string{"AGENTS.md", "infra/AGENTS.md"} {
			if _, err := s.put(id, name, "# "+id+" "+name, 0); err != nil {
				t.Fatal(err)
			}
		}
	}
	s.mu.Lock()
	src := s.db.Sources["alpha"]
	src.ImportPartial = true
	src.ImportDirty = true
	s.db.Sources["alpha"] = src
	s.observed = map[string]time.Time{"alpha": time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	a := s.db.Articles["alpha/infra/AGENTS.md"]
	a.Conflicts = []ImportConflict{{}}
	s.db.Articles["alpha/infra/AGENTS.md"] = a
	s.mu.Unlock()
	key, err := s.createAccessKey("agent", []string{"alpha"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	w := Wiki{Store: s, Token: "owner"}
	call := func(sourceID, target string, public bool) *httptest.ResponseRecorder {
		values := url.Values{"source_id": {sourceID}, "path": {target}}
		r := httptest.NewRequest("GET", "/api/v1/context?"+values.Encode(), nil)
		r.Header.Set("Authorization", "Bearer "+key)
		out := httptest.NewRecorder()
		targetWiki := w
		targetWiki.Public = public
		targetWiki.ServeHTTP(out, r)
		return out
	}
	r := call("alpha", "infra/nested/guide.md", false)
	var ctx agentContext
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &ctx) != nil {
		t.Fatal("context unavailable", r.Code)
	}
	if len(ctx.Instructions) != 2 || ctx.Instructions[0].Path != "AGENTS.md" || ctx.Instructions[1].Path != "infra/AGENTS.md" || len(ctx.Missing) != 1 || ctx.Missing[0] != "infra/nested/AGENTS.md" {
		t.Fatal("incorrect ancestor context")
	}
	if !ctx.SourceStatus.ImportPartial || !ctx.SourceStatus.ImportDirty || ctx.SourceStatus.UnresolvedConflicts != 1 || ctx.SourceStatus.LastImportObservedAt == nil {
		t.Fatal("incorrect source status")
	}
	if call("beta", "infra/guide.md", false).Code != 404 || call("alpha", "infra/guide.md", true).Code != 404 {
		t.Fatal("context escaped scope or public boundary")
	}
	if call("alpha", "../guide.md", false).Code != 400 {
		t.Fatal("invalid path accepted")
	}
}

func TestAgentContextRefusesOversizedInstructionSet(t *testing.T) {
	s := testStore(t)
	source(t, s, "alpha", "private")
	text := strings.Repeat("x", 220000)
	parts := []string{}
	s.mu.Lock()
	for i := 0; i < 7; i++ {
		name := "AGENTS.md"
		if i > 0 {
			name = strings.Join(parts, "/") + "/AGENTS.md"
		}
		s.db.Articles["alpha/"+name] = Article{SourceID: "alpha", Path: name, Versions: []Revision{{Number: 1, Text: text}}}
		parts = append(parts, "d")
	}
	s.mu.Unlock()
	target := strings.Join(parts, "/") + "/guide.md"
	w := Wiki{Store: s, Token: "test-owner"}
	r := request(w, "GET", "/api/v1/context?"+url.Values{"source_id": {"alpha"}, "path": {target}}.Encode(), "", true)
	if r.Code != 413 {
		t.Fatal("oversized context was not rejected", r.Code)
	}
}

func TestPublicAgentContextUsesPublishedRevision(t *testing.T) {
	s := testStore(t)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "AGENTS.md": "# Published instructions"})
	connectFixture(t, s, f, false)
	s.mu.Lock()
	a := s.db.Articles["handbook/AGENTS.md"]
	text := "# Private local instructions"
	a.Versions = append(a.Versions, Revision{Number: 2, Text: text, SHA: digest([]byte(text))})
	a.Conflicts = []ImportConflict{{}}
	s.db.Articles["handbook/AGENTS.md"] = a
	s.observed = map[string]time.Time{"handbook": time.Now()}
	s.mu.Unlock()
	ctx, err := s.agentContext("handbook", "guide.md", true)
	if err != nil || len(ctx.Instructions) != 1 || ctx.Instructions[0].Text != "# Published instructions" || ctx.Instructions[0].Number != 0 {
		t.Fatal("public context revealed a private revision", err)
	}
	if ctx.SourceStatus.LastImportObservedAt != nil || ctx.SourceStatus.UnresolvedConflicts != 0 || ctx.SourceStatus.SyncState != "" || ctx.SourceStatus.SyncError != "" || ctx.SourceStatus.SyncVerification != "" {
		t.Fatal("public context revealed private status")
	}
}

func TestScopedContextReportsConnectionFailure(t *testing.T) {
	s := testStore(t)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "AGENTS.md": "# Context"})
	m := connectFixture(t, s, f, false)
	source(t, s, "other", "private")
	key, err := s.createAccessKey("agent", []string{"handbook"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	f.modify(func() { f.fail = true })
	m.Once(context.Background())
	w := Wiki{Store: s, Token: "owner"}
	call := func(id string) *httptest.ResponseRecorder {
		values := url.Values{"source_id": {id}, "path": {"guide.md"}}
		r := httptest.NewRequest("GET", "/api/v1/context?"+values.Encode(), nil)
		r.Header.Set("Authorization", "Bearer "+key)
		out := httptest.NewRecorder()
		w.ServeHTTP(out, r)
		return out
	}
	r := call("handbook")
	var result agentContext
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &result) != nil {
		t.Fatal("scoped context unavailable", r.Code)
	}
	if result.SourceStatus.SyncState != "error" || result.SourceStatus.SyncError != "upstream_unavailable" || result.SourceStatus.SyncVerification != "unknown" || result.SourceStatus.LastImportObservedAt == nil {
		t.Fatal("connection failure not reflected in context", result.SourceStatus)
	}
	request := httptest.NewRequest("GET", "/api/v1/articles/proposal-base?source_id=handbook&path=AGENTS.md", nil)
	request.Header.Set("Authorization", "Bearer "+key)
	proposal := httptest.NewRecorder()
	w.ServeHTTP(proposal, request)
	var base struct {
		SourceStatus contextStatus `json:"source_status"`
	}
	if proposal.Code != 200 || json.Unmarshal(proposal.Body.Bytes(), &base) != nil || base.SourceStatus.SyncState != "error" || base.SourceStatus.SyncVerification != "unknown" {
		t.Fatal("connection failure not reflected in proposal", proposal.Code)
	}
	if call("other").Code != 404 {
		t.Fatal("connection status escaped source scope")
	}
}
