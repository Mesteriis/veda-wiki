package domain

import (
	"sort"
	"strings"
	"time"
)

// Change is a visible article whose displayed revision was written after a
// point in time.
type Change struct {
	SourceID string `json:"source_id"`
	Path     string `json:"path"`
	Title    string `json:"title"`
	Updated  string `json:"updated_at"`
	SHA      string `json:"sha256"`
}

// ChangesSince lists the pages of snap, optionally of one source, whose
// displayed revision is newer than since, newest first. Deletions are not
// listed: a deleted article is simply absent from the snapshot.
func ChangesSince(snap Snapshot, sourceID string, since time.Time) []Change {
	var out []Change
	for _, p := range snap.Pages {
		if sourceID != "" && p.Environment != sourceID {
			continue
		}
		updated, err := time.Parse(time.RFC3339Nano, p.Updated)
		if err != nil || !updated.After(since) {
			continue
		}
		out = append(out, Change{SourceID: p.Environment, Path: strings.TrimPrefix(p.Path, p.Environment+"/"),
			Title: p.Title, Updated: p.Updated, SHA: p.SHA})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Updated != out[j].Updated {
			ti, _ := time.Parse(time.RFC3339Nano, out[i].Updated)
			tj, _ := time.Parse(time.RFC3339Nano, out[j].Updated)
			return ti.After(tj)
		}
		return out[i].SourceID+"/"+out[i].Path < out[j].SourceID+"/"+out[j].Path
	})
	return out
}
