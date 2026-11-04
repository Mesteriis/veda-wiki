package importer

import (
	"sort"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store"
)

// ConflictRecord is an unresolved import conflict and the article revision
// count a resolution must expect.
type ConflictRecord struct {
	SourceID string                `json:"source_id"`
	Path     string                `json:"path"`
	Index    int                   `json:"index"`
	Revision int                   `json:"revision"`
	Conflict domain.ImportConflict `json:"conflict"`
}

func supersedeImportConflicts(a domain.Article) domain.Article {
	a.Conflicts = append([]domain.ImportConflict(nil), a.Conflicts...)
	for i := range a.Conflicts {
		if a.Conflicts[i].Resolution == "" {
			a.Conflicts[i].Resolution = "superseded"
		}
	}
	return a
}
func hasImportConflict(a domain.Article, hash string, deleted bool) bool {
	for i := len(a.Conflicts) - 1; i >= 0; i-- {
		c := a.Conflicts[i]
		if c.Resolution == "" {
			return c.IncomingSHA == hash && c.Deleted == deleted
		}
	}
	return false
}

// Conflicts lists unresolved import conflicts by source, path and index.
func (s Service) Conflicts() []ConflictRecord {
	out := []ConflictRecord{}
	_ = s.Store.View(func(db *domain.Database) error {
		for _, a := range db.Articles {
			for i, c := range a.Conflicts {
				if c.Resolution == "" {
					out = append(out, ConflictRecord{SourceID: a.SourceID, Path: a.Path, Index: i, Revision: domain.LatestRevision(a), Conflict: c})
				}
			}
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool {
		if out[i].SourceID != out[j].SourceID {
			return out[i].SourceID < out[j].SourceID
		}
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Index < out[j].Index
	})
	return out
}

// ResolveConflict resolves the conflict at index of an article with expected
// revisions, keeping the local revision or taking the upstream one.
func (s Service) ResolveConflict(sourceID, p string, index, expected int, resolution string) error {
	if index < 0 || expected < 1 || (resolution != "local" && resolution != "upstream") {
		return domain.ErrInvalid
	}
	return s.Store.Update(func(tx *store.Tx) error {
		db := tx.DB()
		key := sourceID + "/" + p
		if domain.MoveDeletionLocks(*db, sourceID, p) {
			return domain.ErrConflict
		}
		a, ok := db.Articles[key]
		if !ok {
			return domain.ErrNotFound
		}
		if expected != domain.LatestRevision(a) || index >= len(a.Conflicts) || a.Conflicts[index].Resolution != "" {
			return domain.ErrConflict
		}
		if domain.ConflictResolutionBlocked(*db, sourceID, p) {
			return domain.ErrConflict
		}
		for _, c := range a.Conflicts[index+1:] {
			if c.Resolution == "" {
				return domain.ErrConflict
			}
		}
		conflicts := append([]domain.ImportConflict(nil), a.Conflicts...)
		incoming := conflicts[index]
		for i := 0; i <= index; i++ {
			if conflicts[i].Resolution == "" {
				conflicts[i].Resolution = "superseded"
			}
		}
		conflicts[index].Resolution = resolution
		a.Conflicts = conflicts
		a.UpstreamHash = incoming.IncomingSHA
		if resolution == "upstream" {
			a.Deleted = incoming.Deleted
			if !incoming.Deleted {
				a.Versions = append(append([]domain.Revision(nil), a.Versions...), domain.Revision{
					Number:  domain.LatestRevision(a) + 1,
					Text:    incoming.Incoming,
					SHA:     incoming.IncomingSHA,
					Created: time.Now().UTC().Format(time.RFC3339Nano),
					Assets:  domain.RevisionAssets(incoming.Incoming, p, db.Sources[sourceID]),
				})
			}
		}
		db.Articles[key] = a
		if db.Sources[sourceID].ConnectionID != "" {
			if resolution == "local" {
				last, _ := domain.LastRevision(a)
				domain.EnqueueWrite(db, a, last, time.Now())
			} else {
				domain.SupersedePendingWrites(db, sourceID, p)
			}
		}
		return nil
	})
}
