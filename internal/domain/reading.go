package domain

import (
	"sort"
	"time"
)

// Snapshot lists the current pages a reader may see.
type Snapshot struct {
	Generated string `json:"generated_at"`
	Pages     []Page `json:"pages"`
	Withheld  int    `json:"withheld"`
}

// ReadSnapshot lists the current page of every article a reader may see,
// sorted by key. Public readers see published revisions of connected sources
// and approved translations only, and the snapshot time is that of the newest
// visible revision instead of the state's.
func ReadSnapshot(db Database, public bool, now time.Time) Snapshot {
	snap := Snapshot{Pages: []Page{}}
	if !public {
		snap.Generated = db.Updated
	}
	for key, a := range db.Articles {
		if a.Deleted {
			continue
		}
		src := db.Sources[a.SourceID]
		if public && !SourcePublicReadable(db, src, now) {
			continue
		}
		rev := a.Versions[len(a.Versions)-1]
		if public && src.ConnectionID != "" {
			if a.Published == nil {
				continue
			}
			rev = *a.Published
		}
		original := rev.Text
		rev, _ = DisplayedRevision(a, rev, public)
		if public && rev.Created > snap.Generated {
			snap.Generated = rev.Created
		}
		snap.Pages = append(snap.Pages, Page{Path: key, Title: Title(rev.Text, a.Path), Environment: src.ID, SHA: rev.SHA, Updated: rev.Created, SearchOriginal: original, SearchDisplayed: rev.Text, SearchSourceTitle: src.Title})
	}
	sort.Slice(snap.Pages, func(i, j int) bool { return snap.Pages[i].Path < snap.Pages[j].Path })
	return snap
}

// ReadPage returns the displayed page of one article with its translation
// and conflict state. Public readers never learn whether an unpublished
// translation or an unresolved conflict exists.
func ReadPage(db Database, key string, public bool, now time.Time) (Page, error) {
	a, ok := db.Articles[key]
	if !ok || a.Deleted {
		return Page{}, ErrNotFound
	}
	src := db.Sources[a.SourceID]
	if public && !SourcePublicReadable(db, src, now) {
		return Page{}, ErrNotFound
	}
	r := a.Versions[len(a.Versions)-1]
	if public && src.ConnectionID != "" {
		if a.Published == nil {
			return Page{}, ErrNotFound
		}
		r = *a.Published
	}
	originalSHA := r.SHA
	r, translated := DisplayedRevision(a, r, public)
	origin, translationState := "", ""
	unresolved := false
	if !public {
		for _, conflict := range a.Conflicts {
			if conflict.Resolution == "" {
				unresolved = true
				break
			}
		}
	}
	if src.Kind == "program" {
		translationState = "unavailable"
		if translated {
			translationState = "current"
			origin = a.Translation.Origin
		} else if !public && a.Translation != nil && a.Translation.OriginalSHA != originalSHA {
			// Public readers must not learn whether an unpublished translation exists.
			translationState = "stale"
		}
	}
	blocked := unresolved && !public && ConflictResolutionBlocked(db, a.SourceID, a.Path)
	return Page{Path: key, Title: Title(r.Text, a.Path), Environment: src.ID, SHA: r.SHA, Updated: r.Created, Text: r.Text, OriginalSHA: originalSHA, Translated: translated, TranslationOrigin: origin, TranslationState: translationState, ProgramDocumentation: src.Kind == "program", UnresolvedConflict: unresolved, ConflictResolutionBlocked: blocked}, nil
}

// ReadArticle returns an article record with copied versions. A public reader
// receives only the visible revision, numbered 0, without translation, copy,
// reconciliation, conflict or publication bookkeeping.
func ReadArticle(db Database, sourceID, p string, public bool, now time.Time) (Article, error) {
	a, ok := db.Articles[sourceID+"/"+p]
	if !ok || a.Deleted {
		return Article{}, ErrNotFound
	}
	src := db.Sources[a.SourceID]
	if public && !SourcePublicReadable(db, src, now) {
		return Article{}, ErrNotFound
	}
	a.Versions = append([]Revision(nil), a.Versions...)
	if public && src.ConnectionID != "" {
		if a.Published == nil {
			return Article{}, ErrNotFound
		}
		a.Versions = []Revision{*a.Published}
	}
	if public {
		a.Translation = nil
		a.CopiedFrom = nil
		a.ReconcileBase = nil
		a.Conflicts = nil
		a.UpstreamHash = ""
		a.Versions = a.Versions[len(a.Versions)-1:]
		a.Versions[0].Number = 0
		// The visible revision is already in Versions; do not expose internal
		// publication bookkeeping or a different stored publication snapshot.
		a.Published = nil
		a.PublishedLocal = false
	}
	return a, nil
}

// ReadSources lists the sources a reader may see, sorted by identifier.
// Public readers do not learn connection identifiers; other readers also get
// the synchronization issue and the number of unresolved conflicts. Import
// observations are not part of the state and are left to the caller.
func ReadSources(db Database, public bool, now time.Time) []SourceView {
	out := []SourceView{}
	conflicts := map[string]int{}
	if !public {
		for _, a := range db.Articles {
			for _, c := range a.Conflicts {
				if c.Resolution == "" {
					conflicts[a.SourceID]++
				}
			}
		}
	}
	for _, src := range db.Sources {
		if !public || SourcePublicReadable(db, src, now) {
			if public {
				src.ConnectionID = ""
			}
			view := SourceView{Source: src}
			if !public {
				view.SyncIssue = SourceSyncIssue(db, src, now)
				view.UnresolvedConflicts = conflicts[src.ID]
			}
			out = append(out, view)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
