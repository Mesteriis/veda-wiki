package domain

// KeepRevisions is how many of an article's newest revisions are always kept.
const KeepRevisions = 3

// RevisionsFormat is the store format (Database.Version) whose commits prune
// revision history. It is the newest format; new stores start at it.
const RevisionsFormat = 21

// JobTerminal reports whether an outbox job state is final. Final jobs stay
// in the outbox as a record and never read their revision again.
func JobTerminal(state string) bool {
	return state == "complete" || state == "superseded"
}

// MoveTerminal reports whether a move state is final. Final moves stay as a
// journal record and never read their source or destination revision again.
func MoveTerminal(state string) bool {
	switch state {
	case "complete", "cancelled", "source_changed", "destination_changed":
		return true
	}
	return false
}

// ProtectedRevisions returns the numbers of revisions of article key that a
// record of db may still read and that pruning must keep. Every read of a
// revision other than the newest one is listed here:
//
//   - an outbox job that is not final (JobTerminal) reads revision
//     SyncJob.Revision of its article: the push writes its text
//     (syncer push, pendingBundle) and Validate checks its SHA;
//   - such a job's Bundle names articles a linked copy created as revision 1
//     and holds that revision's SHA. The push reads their newest revision, but
//     Validate checks revision 1 against the bundle SHA, so revision 1 is kept;
//   - a move that is not final (MoveTerminal) reads Move.SourceRevision of its
//     source: removing the source from GitHub sends that text, and Validate
//     checks its SHA;
//   - such a move also reads the first stored revision of its destination:
//     ReconcileMoves, the pull completing a source deletion and the GitHub
//     destination check compare it with Move.SHA (the destination then has
//     only that revision, so this costs nothing);
//   - ReconcileBase of any article names a revision of another article;
//     Validate checks its SHA, and reconciliation moves it forward;
//   - an article of a connected source (Source.ConnectionID set) keeps the
//     newest revision whose SHA is its UpstreamHash: when the remote file
//     differs from the queued write, the push of a job with images, links,
//     a bundle or a directory index (syncer pendingBundle) hashes that
//     revision's text as a git blob to prove the remote still holds the
//     base. It is kept for every connected article, not only those with an
//     active job, because a job can be queued after the base was otherwise
//     old enough to prune. At most one extra revision per article.
//
// Not protected, because nothing reads their text: Article.Published (a full
// copy kept on the article), CopyOrigin (provenance number and SHA only),
// ImportConflict.BaseSHA (compared as a SHA only), final jobs and moves.
// Every other UpstreamHash reader (importer, moves, reconciliation, transfer)
// compares it with a SHA and reads no text. Translations read only
// the newest revision: the worker drops a candidate whose revision is no
// longer the newest, and submissions require the newest expected revision.
// Publishing a job, transfers and conflict resolution likewise act only on
// the newest revision, which is always kept.
func ProtectedRevisions(db Database, key string) map[int]bool {
	protected := map[int]bool{}
	for _, r := range revisionReferences(db) {
		if r.key == key {
			protected[r.number] = true
		}
	}
	return protected
}

// PruneArticle removes revisions of article key that are neither among the
// KeepRevisions newest nor protected, and returns how many it removed.
// Deleted articles follow the same rule.
func PruneArticle(db *Database, key string) (removed int) {
	return pruneArticle(db, key, ProtectedRevisions(*db, key))
}

// PruneAll prunes every article of db and returns how many revisions it
// removed.
func PruneAll(db *Database) (removed int) {
	protected := map[string]map[int]bool{}
	for _, r := range revisionReferences(*db) {
		if protected[r.key] == nil {
			protected[r.key] = map[int]bool{}
		}
		protected[r.key][r.number] = true
	}
	for key := range db.Articles {
		removed += pruneArticle(db, key, protected[key])
	}
	return removed
}

func pruneArticle(db *Database, key string, protected map[int]bool) int {
	a, ok := db.Articles[key]
	if !ok || len(a.Versions) <= KeepRevisions {
		return 0
	}
	newest := len(a.Versions) - KeepRevisions
	// A new slice: the previous one may be shared with committed state.
	kept := make([]Revision, 0, KeepRevisions+len(protected))
	for i, r := range a.Versions {
		if i >= newest || protected[r.Number] {
			kept = append(kept, r)
		}
	}
	removed := len(a.Versions) - len(kept)
	if removed > 0 {
		a.Versions = kept
		db.Articles[key] = a
	}
	return removed
}

type revisionReference struct {
	key    string
	number int
}

// revisionReferences lists every revision a record of db may still read; see
// ProtectedRevisions.
func revisionReferences(db Database) []revisionReference {
	var out []revisionReference
	first := func(key string) {
		if a, ok := db.Articles[key]; ok && len(a.Versions) > 0 {
			out = append(out, revisionReference{key, a.Versions[0].Number})
		}
	}
	revisionOne := func(key string) {
		if _, ok := db.Articles[key]; ok {
			out = append(out, revisionReference{key, 1})
		}
	}
	for _, j := range db.Outbox {
		if JobTerminal(j.State) {
			continue
		}
		out = append(out, revisionReference{j.SourceID + "/" + j.Path, j.Revision})
		for p := range j.Bundle {
			revisionOne(j.SourceID + "/" + p)
		}
	}
	for _, m := range db.Moves {
		if MoveTerminal(m.State) {
			continue
		}
		out = append(out, revisionReference{m.SourceID + "/" + m.SourcePath, m.SourceRevision})
		first(m.DestinationID + "/" + m.DestinationPath)
	}
	for key, a := range db.Articles {
		if b := a.ReconcileBase; b != nil {
			out = append(out, revisionReference{b.SourceID + "/" + b.Path, b.Revision})
		}
		if a.UpstreamHash == "" || db.Sources[a.SourceID].ConnectionID == "" {
			continue
		}
		for i := len(a.Versions) - 1; i >= 0; i-- {
			if a.Versions[i].SHA == a.UpstreamHash {
				out = append(out, revisionReference{key, a.Versions[i].Number})
				break
			}
		}
	}
	return out
}
