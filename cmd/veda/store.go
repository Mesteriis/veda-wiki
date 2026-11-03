package main

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Mesteriis/veda-wiki/internal/store"
)

// Store adds the operations not yet moved into their owning packages to the
// shared store. Each restructure task moves its area's methods out.
type Store struct{ *store.Store }

func openStore(dir string) (*Store, error) {
	st, err := store.Open(dir)
	if err != nil {
		return nil, err
	}
	return &Store{st}, nil
}

func (s *Store) addSource(src Source) error {
	if !identifier.MatchString(src.ID) || strings.TrimSpace(src.Title) == "" || len(src.Title) > 200 || (src.Kind != "wiki" && src.Kind != "program") || (src.Visibility != "private" && src.Visibility != "public") {
		return ErrInvalid
	}
	if src.ConnectionID != "" || src.Metadata != nil || len(src.Assets) != 0 || src.ImportGeneration != "" || src.ImportCommit != "" || src.ImportDirty || src.ImportPartial {
		return ErrInvalid
	}
	s.Lock()
	defer s.Unlock()
	if _, ok := s.DB().Sources[src.ID]; ok {
		return ErrConflict
	}
	db := s.Clone()
	db.Sources[src.ID] = src
	return s.Commit(db)
}
func (s *Store) put(sourceID, p, text string, expected int) (Revision, error) {
	if len(text) > maxArticleBytes || !utf8.ValidString(text) || expected < 0 {
		return Revision{}, ErrInvalid
	}
	s.Lock()
	defer s.Unlock()
	src, ok := s.DB().Sources[sourceID]
	if !ok {
		return Revision{}, ErrNotFound
	}
	if !articlePath(p, src.Kind) {
		return Revision{}, ErrInvalid
	}
	key := sourceID + "/" + p
	if moveDeletionLocks(*s.DB(), sourceID, p) {
		return Revision{}, ErrConflict
	}
	article := s.DB().Articles[key]
	if len(article.Versions) != expected {
		return Revision{}, ErrConflict
	}
	for _, conflict := range article.Conflicts {
		if conflict.Resolution == "" {
			return Revision{}, ErrConflict
		}
	}
	publicNow := sourcePublic(*s.DB(), src, time.Now())
	if publicNow && secretContent.MatchString(text) {
		return Revision{}, ErrInvalid
	}
	sum := sha256.Sum256([]byte(text))
	rev := Revision{Number: expected + 1, Text: text, SHA: hex.EncodeToString(sum[:]), Created: time.Now().UTC().Format(time.RFC3339Nano)}
	versions := append([]Revision(nil), article.Versions...)
	versions = append(versions, rev)
	db := s.Clone()
	article.SourceID, article.Path, article.Versions, article.Deleted = sourceID, p, versions, false
	if publicNow {
		published := rev
		published.Number = 0
		article.Published = &published
		article.PublishedLocal = true
	} else {
		article.PublishedLocal = false
	}
	db.Articles[key] = article
	reconcileMoves(&db)
	if src.ConnectionID != "" {
		enqueueWrite(&db, article, rev, time.Now())
	}
	// Reconciliation can delete another article when a pending move completes.
	// Such writes must persist the whole state, not only this article.
	articleKey := key
	for _, move := range s.DB().Moves {
		switch move.State {
		case "complete", "cancelled", "source_changed", "destination_changed":
			continue
		default:
			articleKey = ""
		}
		break
	}
	if err := s.CommitArticle(db, articleKey); err != nil {
		return Revision{}, err
	}
	return rev, nil
}

func (s *Store) sources(public bool) []SourceView {
	s.RLock()
	defer s.RUnlock()
	out := []SourceView{}
	now := time.Now()
	conflicts := map[string]int{}
	if !public {
		for _, a := range s.DB().Articles {
			for _, c := range a.Conflicts {
				if c.Resolution == "" {
					conflicts[a.SourceID]++
				}
			}
		}
	}
	for _, src := range s.DB().Sources {
		if !public || sourcePublicReadable(*s.DB(), src, now) {
			if public {
				src.ConnectionID = ""
			}
			view := SourceView{Source: src}
			if !public {
				view.SyncIssue = sourceSyncIssue(*s.DB(), src, now)
				if observed, ok := s.Observed(src.ID); ok {
					view.LastImportObservedAt = observed.UTC().Format(time.RFC3339Nano)
				}
				if count, ok := s.Withheld(src.ID); ok {
					view.LastImportWithheld = &count
				}
				view.UnresolvedConflicts = conflicts[src.ID]
			}
			out = append(out, view)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func (s *Store) snapshot(public bool) Snapshot {
	s.RLock()
	defer s.RUnlock()
	snap := Snapshot{Pages: []Page{}}
	if !public {
		snap.Generated = s.DB().Updated
	}
	for key, a := range s.DB().Articles {
		if a.Deleted {
			continue
		}
		src := s.DB().Sources[a.SourceID]
		if public && !sourcePublicReadable(*s.DB(), src, time.Now()) {
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
		rev, _ = displayedRevision(a, rev, public)
		if public && rev.Created > snap.Generated {
			snap.Generated = rev.Created
		}
		snap.Pages = append(snap.Pages, Page{Path: key, Title: title(rev.Text, a.Path), Environment: src.ID, SHA: rev.SHA, Updated: rev.Created, SearchOriginal: original, SearchDisplayed: rev.Text, SearchSourceTitle: src.Title})
	}
	sort.Slice(snap.Pages, func(i, j int) bool { return snap.Pages[i].Path < snap.Pages[j].Path })
	return snap
}
func (s *Store) page(key string, public bool) (Page, error) {
	s.RLock()
	defer s.RUnlock()
	a, ok := s.DB().Articles[key]
	if !ok || a.Deleted {
		return Page{}, ErrNotFound
	}
	src := s.DB().Sources[a.SourceID]
	if public && !sourcePublicReadable(*s.DB(), src, time.Now()) {
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
	r, translated := displayedRevision(a, r, public)
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
	blocked := unresolved && !public && conflictResolutionBlocked(*s.DB(), a.SourceID, a.Path)
	return Page{Path: key, Title: title(r.Text, a.Path), Environment: src.ID, SHA: r.SHA, Updated: r.Created, Text: r.Text, OriginalSHA: originalSHA, Translated: translated, TranslationOrigin: origin, TranslationState: translationState, ProgramDocumentation: src.Kind == "program", UnresolvedConflict: unresolved, ConflictResolutionBlocked: blocked}, nil
}
func (s *Store) article(sourceID, p string, public bool) (Article, error) {
	s.RLock()
	defer s.RUnlock()
	a, ok := s.DB().Articles[sourceID+"/"+p]
	if !ok || a.Deleted {
		return Article{}, ErrNotFound
	}
	src := s.DB().Sources[a.SourceID]
	if public && !sourcePublicReadable(*s.DB(), src, time.Now()) {
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
