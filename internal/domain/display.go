package domain

import (
	"path"
	"strings"
)

// DisplayedRevision returns the revision a reader sees: the article's
// translation when it was made from revision (and is approved for public
// readers when public is set), otherwise revision itself. The flag reports
// whether the translation was chosen.
func DisplayedRevision(a Article, revision Revision, public bool) (Revision, bool) {
	tr := a.Translation
	if tr == nil || tr.OriginalSHA != revision.SHA || (public && !tr.PublicApproved) {
		return revision, false
	}
	return Revision{Number: revision.Number, Text: tr.Text, SHA: tr.SHA, Created: tr.Created}, true
}

// Title returns the text of the first "# " heading in text, or the base
// name of path p when there is none.
func Title(text, p string) string {
	for {
		line, remaining, more := strings.Cut(text, "\n")
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(line[2:])
		}
		if !more {
			return path.Base(p)
		}
		text = remaining
	}
}
