package domain

import "time"

// SourceStatus is what agents are told about the freshness and sync state of
// a source. It states what the service observed, not live infrastructure.
type SourceStatus struct {
	LastImportObservedAt *string `json:"last_import_observed_at"`
	LastImportWithheld   *int    `json:"last_import_withheld,omitempty"`
	ImportPartial        bool    `json:"import_partial"`
	ImportDirty          bool    `json:"import_dirty"`
	UnresolvedConflicts  int     `json:"unresolved_conflicts"`
	UnconfirmedWrites    *int    `json:"unconfirmed_write_jobs,omitempty"`
	PreparedWrites       *int    `json:"prepared_unconfirmed_write_jobs,omitempty"`
	SyncState            string  `json:"sync_state,omitempty"`
	SyncError            string  `json:"sync_error,omitempty"`
	SyncVerification     string  `json:"sync_verification,omitempty"`
	SyncIssue            string  `json:"sync_issue,omitempty"`
}

// BuildSourceStatus reads only db and the supplied import observations (nil
// when the source has none). The caller holds the store lock or operates on a
// request-local snapshot, and has authorized the source.
func BuildSourceStatus(db Database, sourceID string, src Source, now time.Time, observedAt *time.Time, withheld *int) SourceStatus {
	status := SourceStatus{ImportPartial: src.ImportPartial, ImportDirty: src.ImportDirty, SyncIssue: SourceSyncIssue(db, src, now)}
	if conn, ok := db.Connections[src.ConnectionID]; src.ConnectionID != "" && ok {
		status.SyncState = conn.State
		status.SyncError = conn.LastError
		status.SyncVerification = conn.Verification
		count, prepared := 0, 0
		for _, job := range db.Outbox {
			if job.SourceID == sourceID && !JobTerminal(job.State) {
				count++
				if job.PreparedCommit != "" || job.ScopePrepared {
					prepared++
				}
			}
		}
		status.UnconfirmedWrites = &count
		status.PreparedWrites = &prepared
	}
	if observedAt != nil {
		value := observedAt.UTC().Format(time.RFC3339Nano)
		status.LastImportObservedAt = &value
	}
	if withheld != nil {
		count := *withheld
		status.LastImportWithheld = &count
	}
	for _, article := range db.Articles {
		if article.SourceID != sourceID {
			continue
		}
		for _, conflict := range article.Conflicts {
			if conflict.Resolution == "" {
				status.UnresolvedConflicts++
			}
		}
	}
	return status
}
