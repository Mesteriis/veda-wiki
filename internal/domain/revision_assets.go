package domain

import "time"

// RevisionAssets returns the images of source that text, written at
// articlePath, references: the set a new revision or publication copy keeps.
// It is nil when the text references none of the source's images.
func RevisionAssets(text, articlePath string, source Source) map[string]BlobRef {
	refs := RetainedPendingAssets(text, articlePath, source.Assets)
	if len(refs) == 0 {
		return nil
	}
	return refs
}

// RevisionImage reports whether the image name of source sourceID with
// digest sha belongs to a version of an article that the reader may see, and
// returns its reference. A public reader sees only the version ReadPage shows
// to the public: the publication copy of a connected source, or the latest
// revision of a local one, and only while the source is publicly readable.
// The owner sees every stored revision and the publication copy of articles
// that are not deleted; any other private reader sees only the latest
// revision, as ReadPage shows it, since readers cannot read revision history.
// Callers answer every refusal identically, so a request does not learn
// whether an image it may not see exists.
func RevisionImage(db Database, sourceID, name, sha string, public, owner bool, now time.Time) (BlobRef, bool) {
	if _, err := HexKey(sha); err != nil {
		return BlobRef{}, false
	}
	src, ok := db.Sources[sourceID]
	if !ok || (public && !SourcePublicReadable(db, src, now)) {
		return BlobRef{}, false
	}
	for _, a := range db.Articles {
		if a.SourceID != sourceID || a.Deleted {
			continue
		}
		for _, rev := range shownRevisions(a, src, public, owner) {
			if ref, found := rev.Assets[name]; found && ref.SHA == sha {
				return ref, true
			}
		}
	}
	return BlobRef{}, false
}

// shownRevisions lists the versions of a whose images a reader may load.
func shownRevisions(a Article, src Source, public, owner bool) []Revision {
	if public {
		if rev, visible := PublicRevision(a, src); visible {
			return []Revision{rev}
		}
		return nil
	}
	if !owner {
		if rev, found := LastRevision(a); found {
			return []Revision{rev}
		}
		return nil
	}
	if a.Published == nil {
		return a.Versions
	}
	return append(append([]Revision(nil), a.Versions...), *a.Published)
}

// PublicRevision is the version of a that a public reader sees, before any
// translation: the publication copy of a connected source, or the latest
// revision of a local one. It reports false when a connected source's article
// has no publication copy. The caller checks SourcePublicReadable.
func PublicRevision(a Article, src Source) (Revision, bool) {
	if src.ConnectionID != "" {
		if a.Published == nil {
			return Revision{}, false
		}
		return *a.Published, true
	}
	rev, _ := LastRevision(a)
	return rev, true
}
