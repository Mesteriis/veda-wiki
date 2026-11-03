package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestScopedMoveLocksMatchOwnerWithoutRevealingOtherSource(t *testing.T) {
	s := testStore(t)
	revisions := map[string]Revision{}
	for _, id := range []string{"source", "destination"} {
		programSource(t, s, id, "private")
		for _, p := range []string{"docs/item.md", "docs/dependency.md", "docs/unrelated.md"} {
			rev, err := s.put(id, p, "# Original", 0)
			if err != nil {
				t.Fatal(err)
			}
			revisions[id+"/"+p] = rev
		}
	}
	keys := map[string]string{}
	for _, id := range []string{"source", "destination"} {
		key, err := s.createAccessKey(id, []string{id}, time.Now().Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		keys[id] = key
	}
	s.Lock()
	s.DB().Moves = map[string]Move{"private-move-id": {State: "deleting_source", SourceID: "source", SourcePath: "docs/item.md", DestinationID: "destination", DestinationPath: "docs/item.md", Links: map[string]string{"docs/dependency.md": "private-link-hash"}, PreparedDeletion: "private-commit-id"}}
	s.Unlock()
	for _, tc := range []struct {
		source, path string
		blocked      bool
	}{
		{"source", "docs/item.md", true}, {"source", "docs/dependency.md", false},
		{"destination", "docs/item.md", true}, {"destination", "docs/dependency.md", true},
		{"destination", "docs/unrelated.md", false},
	} {
		view, valid, err := s.scopedView(keys[tc.source], time.Now())
		if err != nil || !valid {
			t.Fatal("scoped view unavailable", err)
		}
		result, err := view.proposalBase(tc.source, tc.path)
		if err != nil || result["write_blocked_by_move"] != tc.blocked {
			t.Fatal("incorrect scoped move status", tc.source, tc.path, err)
		}
		ownerResult, err := s.proposalBase(tc.source, tc.path)
		if err != nil || ownerResult["write_blocked_by_move"] != tc.blocked {
			t.Fatal("incorrect owner move status", tc.source, tc.path, err)
		}
		encoded, err := json.Marshal(result)
		if err != nil || strings.Contains(string(encoded), "private-move-id") || strings.Contains(string(encoded), "private-commit-id") || strings.Contains(string(encoded), "private-link-hash") {
			t.Fatal("move details exposed")
		}
		other := "source"
		if tc.source == "source" {
			other = "destination"
		}
		if strings.Contains(string(encoded), `"`+other+`"`) {
			t.Fatal("other source exposed")
		}
		rev := revisions[tc.source+"/"+tc.path]
		_, err = view.validateTranslation(translationRequest{SourceID: tc.source, Path: tc.path, ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Перевод"})
		if tc.blocked && err != ErrConflict || !tc.blocked && err != nil {
			t.Fatal("scoped translation lock mismatch", tc.source, tc.path, err)
		}
	}
}
