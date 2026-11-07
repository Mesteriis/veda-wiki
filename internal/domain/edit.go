package domain

import (
	"fmt"
	"strings"
	"time"
)

// ErrFragmentNotFound reports an edit whose fragment is absent from the
// current revision. It is also ErrInvalid.
var ErrFragmentNotFound = fmt.Errorf("%w: fragment not found", ErrInvalid)

// ErrFragmentAmbiguous reports an edit whose fragment occurs more than once,
// overlapping occurrences included. It is also ErrInvalid.
var ErrFragmentAmbiguous = fmt.Errorf("%w: fragment occurs more than once", ErrInvalid)

// ReplaceFragment replaces the only occurrence of old in text with replacement.
func ReplaceFragment(text, old, replacement string) (string, error) {
	if old == "" {
		return "", ErrInvalid
	}
	at := strings.Index(text, old)
	if at < 0 {
		return "", ErrFragmentNotFound
	}
	if strings.Contains(text[at+1:], old) {
		return "", ErrFragmentAmbiguous
	}
	return text[:at] + replacement + text[at+len(old):], nil
}

// EditArticleVia writes a revision that replaces one unique fragment of the
// current revision. The expected revision is checked before the fragment, so
// a stale edit is a conflict rather than a missing fragment; the write itself
// follows PutArticleVia.
func EditArticleVia(db *Database, sourceID, p, old, replacement string, expected int, via string, now time.Time) (Revision, string, error) {
	article, ok := db.Articles[sourceID+"/"+p]
	if !ok || article.Deleted || expected < 1 {
		return Revision{}, "", ErrNotFound
	}
	if LatestRevision(article) != expected {
		return Revision{}, "", ErrConflict
	}
	current, ok := LastRevision(article)
	if !ok {
		return Revision{}, "", ErrNotFound
	}
	text, err := ReplaceFragment(current.Text, old, replacement)
	if err != nil {
		return Revision{}, "", err
	}
	return PutArticleVia(db, sourceID, p, text, expected, via, now)
}
