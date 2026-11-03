package domain

import (
	"path"
	"strings"
)

func DisplayedRevision(a Article, revision Revision, public bool) (Revision, bool) {
	tr := a.Translation
	if tr == nil || tr.OriginalSHA != revision.SHA || (public && !tr.PublicApproved) {
		return revision, false
	}
	return Revision{Number: revision.Number, Text: tr.Text, SHA: tr.SHA, Created: tr.Created}, true
}

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
