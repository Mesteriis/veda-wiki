package domain

// LatestRevision is the number of the article's newest revision, 0 when it has
// none. Revision numbers are monotonic and may have gaps once old revisions
// are pruned, so the count of stored revisions is not the current revision.
func LatestRevision(a Article) int {
	if len(a.Versions) == 0 {
		return 0
	}
	return a.Versions[len(a.Versions)-1].Number
}

// RevisionByNumber returns the stored revision with the given number.
func RevisionByNumber(a Article, n int) (Revision, bool) {
	for _, r := range a.Versions {
		if r.Number == n {
			return r, true
		}
	}
	return Revision{}, false
}

// LastRevision returns the newest stored revision.
func LastRevision(a Article) (Revision, bool) {
	if len(a.Versions) == 0 {
		return Revision{}, false
	}
	return a.Versions[len(a.Versions)-1], true
}

func lastHasSHA(a Article, sha string) bool {
	r, ok := LastRevision(a)
	return ok && r.SHA == sha
}
