package domain

import (
	"encoding/json"
	"testing"
)

// The expected documents were produced by json.Marshal of the same values with
// the pre-extraction types in cmd/veda, whose search, scope and sync-issue
// fields were unexported. Exporting them with `json:"-"` must not change the
// serialized form stored on disk or returned by the API.
func TestRenamedFieldsKeepJSONShape(t *testing.T) {
	withheld := 3
	for _, tc := range []struct {
		name  string
		value any
		want  string
	}{
		{"Page", Page{SearchText: "o", SearchSourceTitle: "s", ProgramDocumentation: true, UnresolvedConflict: true, ConflictResolutionBlocked: true, Path: "src/a.md", Title: "A", Environment: "src", SHA: "def", Updated: "2026-01-01T00:00:00Z", Text: "# A", HTML: "<h1>A</h1>"},
			`{"program_documentation":true,"unresolved_conflict":true,"conflict_resolution_blocked_by_write":true,"path":"src/a.md","title":"A","environment":"src","sha256":"def","updated_at":"2026-01-01T00:00:00Z"}`},
		{"SyncJob", SyncJob{ScopePrepared: true, ScopeBundle: true, ScopeBundlePaths: map[string]bool{"b.md": true}, ID: "src/a.md@1", SourceID: "src", Path: "a.md", Revision: 1, SHA: "def", State: "pending", Created: "2026-01-01T00:00:00Z", Attempts: 2, LastError: "e", PreparedCommit: "p", ObservedCommit: "o", NextAttempt: "n", Visibility: "public", Assets: map[string]BlobRef{"i.png": {SHA: "x", MIME: "image/png"}}, Links: map[string]string{"l.md": "y"}, Bundle: map[string]string{"b.md": "z"}},
			`{"id":"src/a.md@1","source_id":"src","path":"a.md","revision":1,"sha256":"def","state":"pending","created_at":"2026-01-01T00:00:00Z","attempts":2,"last_error":"e","prepared_commit":"p","observed_commit":"o","next_attempt":"n","visibility":"public","assets":{"i.png":{"sha256":"x","mime":"image/png"}},"links":{"l.md":"y"},"bundle":{"b.md":"z"}}`},
		{"SourceView", SourceView{Source: Source{ID: "src", Title: "Src", Kind: "wiki", Visibility: "public", ConnectionID: "src"}, LastImportObservedAt: "2026-01-01T00:00:00Z", LastImportWithheld: &withheld, UnresolvedConflicts: 2, SyncIssue: "paused"},
			`{"id":"src","title":"Src","kind":"wiki","visibility":"public","connection_id":"src","last_import_observed_at":"2026-01-01T00:00:00Z","last_import_withheld":3,"unresolved_conflicts":2}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.Marshal(tc.value)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Fatalf("json = %s\nwant   %s", got, tc.want)
			}
		})
	}
}
