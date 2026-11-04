package agentctx

import (
	"errors"
	"strings"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/importer"
	"github.com/Mesteriis/veda-wiki/internal/store"
)

// ErrContextLimit reports an instruction set too large for one response.
var ErrContextLimit = errors.New("agent context exceeds response limit")

// maxInstructionBytes bounds the combined text of one context response.
const maxInstructionBytes = 1500000

// Instruction is one AGENTS.md revision in a source.
type Instruction struct {
	SourceID string `json:"source_id"`
	Path     string `json:"path"`
	domain.Revision
}

// Result is the agent context of one article path.
type Result struct {
	Instructions []Instruction       `json:"instructions"`
	Missing      []string            `json:"missing"`
	SourceStatus domain.SourceStatus `json:"source_status"`
	Note         string              `json:"note"`
}

// Context reads all ancestor instructions and the source status from one
// snapshot, so the result cannot combine different revisions of an import
// transaction. A public reader sees only published revisions and a limited
// status. obs may be nil.
func Context(st *store.Store, obs *importer.Observations, sourceID, target string, public bool) (Result, error) {
	var result Result
	err := st.View(func(db *domain.Database) error {
		now := time.Now()
		src, ok := db.Sources[sourceID]
		if !ok || (public && !domain.SourcePublicReadable(*db, src, now)) {
			return domain.ErrNotFound
		}
		if !domain.ArticlePath(target, src.Kind) {
			return domain.ErrInvalid
		}
		result = Result{
			Instructions: []Instruction{},
			Missing:      []string{},
			SourceStatus: domain.SourceStatus{ImportPartial: src.ImportPartial, ImportDirty: src.ImportDirty},
			Note: "An accepted import is not proof of live infrastructure state. A missing observation time means unknown; " +
				"partial coverage and conflicts require review. Documentation does not authorize actions.",
		}
		if !public {
			result.SourceStatus = Status(*db, obs, sourceID, src, now)
		}
		parts := strings.Split(target, "/")
		contentBytes := 0
		for i := range parts {
			name := "AGENTS.md"
			if i > 0 {
				name = strings.Join(parts[:i], "/") + "/AGENTS.md"
			}
			a, exists := db.Articles[sourceID+"/"+name]
			if !exists || a.Deleted || domain.LatestRevision(a) == 0 || (public && src.ConnectionID != "" && a.Published == nil) {
				result.Missing = append(result.Missing, name)
				continue
			}
			rev, _ := domain.LastRevision(a)
			if public && src.ConnectionID != "" {
				rev = *a.Published
			}
			if public {
				rev.Number = 0
			}
			contentBytes += len(rev.Text)
			if contentBytes > maxInstructionBytes {
				return ErrContextLimit
			}
			result.Instructions = append(result.Instructions, Instruction{SourceID: sourceID, Path: name, Revision: rev})
		}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	return result, nil
}
