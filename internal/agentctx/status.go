// Package agentctx builds what agents are shown before they read or propose
// changes: root and ancestor AGENTS.md instructions, source status and the
// bases of write proposals. Every function reads one store snapshot.
package agentctx

import (
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/importer"
)

// Status reports src from db and obs only. obs may be nil. The caller has
// authorized the source and reads db from a snapshot.
func Status(db domain.Database, obs *importer.Observations, sourceID string, src domain.Source, now time.Time) domain.SourceStatus {
	var observedAt *time.Time
	var withheld *int
	if at, ok := obs.Observed(sourceID); ok {
		observedAt = &at
	}
	if count, ok := obs.Withheld(sourceID); ok {
		withheld = &count
	}
	return domain.BuildSourceStatus(db, sourceID, src, now, observedAt, withheld)
}
