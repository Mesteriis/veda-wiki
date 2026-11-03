package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
	"unicode/utf8"
)

// ValidNewSource reports whether src is an acceptable new local source: a
// valid identifier, title, kind and visibility, and none of the fields that
// only imports and GitHub connections set.
func ValidNewSource(src Source) bool {
	if !idPattern.MatchString(src.ID) || strings.TrimSpace(src.Title) == "" || len(src.Title) > 200 || (src.Kind != "wiki" && src.Kind != "program") || (src.Visibility != "private" && src.Visibility != "public") {
		return false
	}
	return src.ConnectionID == "" && src.Metadata == nil && len(src.Assets) == 0 && src.ImportGeneration == "" && src.ImportCommit == "" && !src.ImportDirty && !src.ImportPartial
}

// AddSource adds a new local source. It refuses an invalid source and an
// existing identifier.
func AddSource(db *Database, src Source) error {
	if !ValidNewSource(src) {
		return ErrInvalid
	}
	if _, ok := db.Sources[src.ID]; ok {
		return ErrConflict
	}
	db.Sources[src.ID] = src
	return nil
}

// ValidArticleText reports whether text and the expected revision count can
// be written at all, before the state is consulted.
func ValidArticleText(text string, expected int) bool {
	return len(text) <= MaxArticleBytes && utf8.ValidString(text) && expected >= 0
}

// PutArticle appends a revision to an article when expected matches its
// revision count. It publishes the revision of a public source at once,
// queues it for a connected source and reconciles pending moves.
//
// key is the only article record the change touches, or "" when the whole
// state must be persisted: completing a pending move can delete another
// article. now is the revision's time and decides publication.
func PutArticle(db *Database, sourceID, p, text string, expected int, now time.Time) (rev Revision, key string, err error) {
	if !ValidArticleText(text, expected) {
		return Revision{}, "", ErrInvalid
	}
	src, ok := db.Sources[sourceID]
	if !ok {
		return Revision{}, "", ErrNotFound
	}
	if !ArticlePath(p, src.Kind) {
		return Revision{}, "", ErrInvalid
	}
	articleKey := sourceID + "/" + p
	if MoveDeletionLocks(*db, sourceID, p) {
		return Revision{}, "", ErrConflict
	}
	article := db.Articles[articleKey]
	if len(article.Versions) != expected {
		return Revision{}, "", ErrConflict
	}
	for _, conflict := range article.Conflicts {
		if conflict.Resolution == "" {
			return Revision{}, "", ErrConflict
		}
	}
	publicNow := SourcePublic(*db, src, now)
	if publicNow && credentialPattern.MatchString(text) {
		return Revision{}, "", ErrInvalid
	}
	// Reconciliation can delete another article when a pending move
	// completes. Such writes must persist the whole state, not only this
	// article; decide that from the moves before reconciliation.
	key = articleKey
	for _, move := range db.Moves {
		switch move.State {
		case "complete", "cancelled", "source_changed", "destination_changed":
			continue
		default:
			key = ""
		}
		break
	}
	sum := sha256.Sum256([]byte(text))
	rev = Revision{Number: expected + 1, Text: text, SHA: hex.EncodeToString(sum[:]), Created: now.UTC().Format(time.RFC3339Nano)}
	versions := append([]Revision(nil), article.Versions...)
	versions = append(versions, rev)
	article.SourceID, article.Path, article.Versions, article.Deleted = sourceID, p, versions, false
	if publicNow {
		published := rev
		published.Number = 0
		article.Published = &published
		article.PublishedLocal = true
	} else {
		article.PublishedLocal = false
	}
	db.Articles[articleKey] = article
	ReconcileMoves(db)
	if src.ConnectionID != "" {
		EnqueueWrite(db, article, rev, now)
	}
	return rev, key, nil
}
