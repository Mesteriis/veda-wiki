package translation

import (
	"reflect"
	"sort"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store"
)

// Submit stores an owner's translation. A translation of a public article
// needs ConfirmPublication.
func (s Service) Submit(in Request) (domain.Translation, error) {
	return s.save(in, false)
}

// SaveDraft stores an automatic translation. Automatic translations of
// public articles remain private drafts until an owner explicitly approves
// publication through Submit.
func (s Service) SaveDraft(in Request) (domain.Translation, error) {
	return s.save(in, true)
}

func (s Service) save(in Request, privateDraft bool) (domain.Translation, error) {
	if !in.valid() {
		return domain.Translation{}, domain.ErrInvalid
	}
	var tr domain.Translation
	err := s.Store.Update(func(tx *store.Tx) error {
		db := tx.DB()
		src, ok := db.Sources[in.SourceID]
		if !ok {
			return domain.ErrNotFound
		}
		if src.Kind != "program" || !domain.ArticlePath(in.Path, src.Kind) {
			return domain.ErrInvalid
		}
		key := in.SourceID + "/" + in.Path
		if domain.MoveDeletionLocks(*db, in.SourceID, in.Path) {
			return domain.ErrConflict
		}
		a, ok := db.Articles[key]
		if !ok || a.Deleted {
			return domain.ErrNotFound
		}
		if len(a.Versions) != in.ExpectedRevision {
			return domain.ErrConflict
		}
		rev := a.Versions[len(a.Versions)-1]
		if in.OriginalSHA != rev.SHA {
			return domain.ErrConflict
		}
		if in.ExpectedTranslationFingerprint != nil && *in.ExpectedTranslationFingerprint != domain.TranslationFingerprint(a.Translation) {
			return domain.ErrConflict
		}
		if privateDraft && a.Translation != nil && a.Translation.OriginalSHA == rev.SHA {
			return domain.ErrConflict
		}
		for _, c := range a.Conflicts {
			if c.Resolution == "" {
				return domain.ErrConflict
			}
		}
		if !reflect.DeepEqual(protectedMarkdown(rev.Text), protectedMarkdown(in.Text)) {
			return domain.ErrInvalid
		}
		public := domain.SourcePublic(*db, src, time.Now())
		if public && ((!in.ConfirmPublication && !privateDraft) || domain.CredentialLike(in.Text)) {
			return domain.ErrInvalid
		}
		origin := ""
		if privateDraft {
			origin = "local_model"
		} else if a.Translation != nil && a.Translation.OriginalSHA == rev.SHA && a.Translation.Text == in.Text {
			// Approval of an unchanged draft does not change its provenance.
			origin = a.Translation.Origin
		}
		tr = domain.Translation{Language: "ru", OriginalSHA: rev.SHA, Text: in.Text, SHA: domain.Digest([]byte(in.Text)), Created: time.Now().UTC().Format(time.RFC3339Nano), PublicApproved: public && in.ConfirmPublication, Origin: origin}
		a.Translation = &tr
		if origin != "" && db.Version < 11 {
			db.Version = 11
		}
		db.Articles[key] = a
		domain.ReconcileMoves(db)
		return nil
	})
	if err != nil {
		return domain.Translation{}, err
	}
	return tr, nil
}

// Candidate is an article of a program source that needs a translation or a
// publication review.
type Candidate struct {
	SourceID    string `json:"source_id"`
	Path        string `json:"path"`
	Revision    int    `json:"revision"`
	OriginalSHA string `json:"original_sha256"`
	Visibility  string `json:"visibility"`
	State       string `json:"state"`
}

// Candidates lists the articles that lack a current translation, or whose
// draft awaits publication review, ordered by source and path.
func (s Service) Candidates() []Candidate {
	out := []Candidate{}
	_ = s.Store.View(func(db *domain.Database) error {
		for _, a := range db.Articles {
			src := db.Sources[a.SourceID]
			if src.Kind != "program" || a.Deleted {
				continue
			}
			blocked := false
			for _, c := range a.Conflicts {
				if c.Resolution == "" {
					blocked = true
					break
				}
			}
			r := a.Versions[len(a.Versions)-1]
			visibility := "private"
			if domain.SourcePublic(*db, src, time.Now()) {
				visibility = "public"
			}
			state := "needed"
			if a.Translation != nil && a.Translation.OriginalSHA == r.SHA {
				if visibility != "public" || a.Translation.PublicApproved {
					continue
				}
				state = "publication_review"
			}
			if blocked {
				state = "blocked_conflict"
			}
			out = append(out, Candidate{SourceID: a.SourceID, Path: a.Path, Revision: r.Number, OriginalSHA: r.SHA, Visibility: visibility, State: state})
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool {
		if out[i].SourceID != out[j].SourceID {
			return out[i].SourceID < out[j].SourceID
		}
		return out[i].Path < out[j].Path
	})
	return out
}
