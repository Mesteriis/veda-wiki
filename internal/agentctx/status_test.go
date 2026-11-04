package agentctx

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/importer"
)

func nowText() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func TestSourceStatusJSONShape(t *testing.T) {
	observed := time.Date(2026, 1, 2, 3, 4, 5, 600, time.UTC)
	withheld, unconfirmed, prepared := 3, 2, 1
	at := observed.Format(time.RFC3339Nano)
	full, err := json.Marshal(domain.SourceStatus{LastImportObservedAt: &at, LastImportWithheld: &withheld, ImportPartial: true, ImportDirty: true, UnresolvedConflicts: 4, UnconfirmedWrites: &unconfirmed, PreparedWrites: &prepared, SyncState: "error", SyncError: "boom", SyncVerification: "verified", SyncIssue: "error"})
	if err != nil {
		t.Fatal(err)
	}
	const wantFull = `{"last_import_observed_at":"2026-01-02T03:04:05.0000006Z","last_import_withheld":3,"import_partial":true,"import_dirty":true,"unresolved_conflicts":4,"unconfirmed_write_jobs":2,"prepared_unconfirmed_write_jobs":1,"sync_state":"error","sync_error":"boom","sync_verification":"verified","sync_issue":"error"}`
	if string(full) != wantFull {
		t.Fatal("status JSON changed", string(full))
	}
	empty, _ := json.Marshal(domain.SourceStatus{})
	if string(empty) != `{"last_import_observed_at":null,"import_partial":false,"import_dirty":false,"unresolved_conflicts":0}` {
		t.Fatal("empty status JSON changed", string(empty))
	}
}

func TestStatusReadsOnlyTheGivenSnapshotAndObservations(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	db := domain.Database{
		Connections: map[string]domain.GitHubConnection{"c": {State: "conflict", LastError: "x", Verification: "verified"}},
		Outbox: map[string]domain.SyncJob{
			"open":     {SourceID: "s", State: "retry", PreparedCommit: "p"},
			"scoped":   {SourceID: "s", State: "pending", ScopePrepared: true},
			"plain":    {SourceID: "s", State: "pending"},
			"complete": {SourceID: "s", State: "complete", PreparedCommit: "p"},
			"other":    {SourceID: "t", State: "pending"},
		},
		Articles: map[string]domain.Article{
			"s/a.md": {SourceID: "s", Conflicts: []domain.ImportConflict{{}, {Resolution: "keep"}}},
			"t/a.md": {SourceID: "t", Conflicts: []domain.ImportConflict{{}}},
		},
	}
	src := domain.Source{ID: "s", ConnectionID: "c", ImportDirty: true}
	obs := &importer.Observations{}
	obs.MarkObserved("s", now)
	obs.MarkWithheld("s", 5)
	got := Status(db, obs, "s", src, now)
	if got.SyncState != "conflict" || got.SyncError != "x" || got.SyncVerification != "verified" || got.SyncIssue != "attention" || !got.ImportDirty || got.ImportPartial {
		t.Fatal("connection fields", got)
	}
	if got.UnconfirmedWrites == nil || *got.UnconfirmedWrites != 3 || got.PreparedWrites == nil || *got.PreparedWrites != 2 || got.UnresolvedConflicts != 1 {
		t.Fatal("queue and conflict counts", got)
	}
	if got.LastImportObservedAt == nil || *got.LastImportObservedAt != "2026-01-01T00:00:00Z" || got.LastImportWithheld == nil || *got.LastImportWithheld != 5 {
		t.Fatal("observations", got)
	}
	local := Status(db, nil, "s", domain.Source{ID: "s"}, now)
	if local.UnconfirmedWrites != nil || local.PreparedWrites != nil || local.LastImportObservedAt != nil || local.LastImportWithheld != nil || local.SyncState != "" {
		t.Fatal("local source or missing observations reported connection data", local)
	}
	db.Outbox = nil
	if got = Status(db, obs, "s", src, now); *got.UnconfirmedWrites != 0 {
		t.Fatal("status ignored the snapshot it was given", got)
	}
}
