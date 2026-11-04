package knowledge

import (
	"sort"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// Scope is what a request may read beyond domain's reader rules. The sources
// of a scoped key, scoped session or team user are already restricted by the
// request snapshot the caller passes to Allowed; Scope does not repeat them.
type Scope struct {
	// Owner adds the published copy of an article when it differs from the
	// current revision. It is ignored for public requests.
	Owner bool
}

// AllowedSet is the set of snapshots one request may read. The zero value
// allows nothing.
type AllowedSet struct {
	keys map[SnapshotKey]struct{}
}

// Has reports whether the request may read the snapshot.
func (s AllowedSet) Has(k SnapshotKey) bool {
	_, ok := s.keys[k]
	return ok
}

// Len is the number of allowed snapshots.
func (s AllowedSet) Len() int { return len(s.keys) }

// Keys lists the allowed snapshots sorted by source, path and SHA.
func (s AllowedSet) Keys() []SnapshotKey {
	out := make([]SnapshotKey, 0, len(s.keys))
	for k := range s.keys {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return keyLess(out[i], out[j]) })
	return out
}

func keyLess(a, b SnapshotKey) bool {
	if a.SourceID != b.SourceID {
		return a.SourceID < b.SourceID
	}
	if a.Path != b.Path {
		return a.Path < b.Path
	}
	return a.SHA < b.SHA
}

// Allowed builds the snapshots a request may read from the request-scoped
// database, with public computed by the caller as !owner && !reader. It
// applies domain.ReadSnapshot unchanged: public requests get the
// PublicRevision of articles in publicly readable sources (deleted articles,
// partial imports, expired or stale GitHub visibility proofs and pending
// heads are excluded there); other requests get the current revision of every
// article in the snapshot. A non-public owner also gets the published copy
// when its SHA differs from the current one.
func Allowed(db domain.Database, public bool, scope Scope, now time.Time) AllowedSet {
	set := AllowedSet{keys: map[SnapshotKey]struct{}{}}
	for _, page := range domain.ReadSnapshot(db, public, now).Pages {
		a := db.Articles[page.Path]
		if page.SHA != "" {
			set.keys[SnapshotKey{SourceID: a.SourceID, Path: a.Path, SHA: page.SHA}] = struct{}{}
		}
		if public || !scope.Owner {
			continue
		}
		if published, ok := domain.PublicRevision(a, db.Sources[a.SourceID]); ok && published.SHA != "" && published.SHA != page.SHA {
			set.keys[SnapshotKey{SourceID: a.SourceID, Path: a.Path, SHA: published.SHA}] = struct{}{}
		}
	}
	return set
}

// ActiveSnapshot is one text to index, with an immutable copy of the text.
// Current marks the newest revision. PublicationCopy marks the revision
// domain.PublicRevision selects: the stored publication copy of a GitHub
// source, or the newest revision of a local one. It says nothing about
// whether anyone may read it now; Allowed decides that per request. One
// snapshot may be both.
type ActiveSnapshot struct {
	Key             SnapshotKey
	Text            string
	Current         bool
	PublicationCopy bool
}

// ActiveSnapshots lists every snapshot to index: the current revision and,
// when its SHA differs, the published copy of each article that is not
// deleted, sorted by key. It is exactly what an owner may read. The published
// copy is listed whether or not the source is publicly readable right now,
// so an expiring visibility proof does not cause reindexing; Allowed decides
// visibility on every request.
func ActiveSnapshots(db domain.Database) []ActiveSnapshot {
	var out []ActiveSnapshot
	for _, a := range db.Articles {
		out = append(out, articleSnapshots(db, a)...)
	}
	sort.Slice(out, func(i, j int) bool { return keyLess(out[i].Key, out[j].Key) })
	return out
}

// ActiveSnapshotOf is the active snapshot with key k, if k is still one of
// ActiveSnapshots(db).
func ActiveSnapshotOf(db domain.Database, k SnapshotKey) (ActiveSnapshot, bool) {
	a, ok := db.Articles[k.Article()]
	if !ok || a.SourceID != k.SourceID || a.Path != k.Path {
		return ActiveSnapshot{}, false
	}
	for _, s := range articleSnapshots(db, a) {
		if s.Key == k {
			return s, true
		}
	}
	return ActiveSnapshot{}, false
}

func articleSnapshots(db domain.Database, a domain.Article) []ActiveSnapshot {
	if a.Deleted {
		return nil
	}
	current, ok := domain.LastRevision(a)
	if !ok || current.SHA == "" {
		return nil
	}
	published, hasPublished := domain.PublicRevision(a, db.Sources[a.SourceID])
	same := hasPublished && published.SHA == current.SHA
	out := []ActiveSnapshot{{Key: SnapshotKey{SourceID: a.SourceID, Path: a.Path, SHA: current.SHA}, Text: current.Text, Current: true, PublicationCopy: same}}
	if hasPublished && !same && published.SHA != "" {
		out = append(out, ActiveSnapshot{Key: SnapshotKey{SourceID: a.SourceID, Path: a.Path, SHA: published.SHA}, Text: published.Text, PublicationCopy: true})
	}
	return out
}
