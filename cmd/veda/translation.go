package main

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yuin/goldmark/ast"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

type Translation struct {
	Origin         string `json:"origin,omitempty"`
	Language       string `json:"language"`
	OriginalSHA    string `json:"original_sha256"`
	Text           string `json:"text"`
	SHA            string `json:"sha256"`
	Created        string `json:"created_at"`
	PublicApproved bool   `json:"public_approved"`
}
type translationRequest struct {
	SourceID                       string  `json:"source_id"`
	Path                           string  `json:"path"`
	ExpectedRevision               int     `json:"expected_revision"`
	OriginalSHA                    string  `json:"original_sha256"`
	Text                           string  `json:"text"`
	ConfirmPublication             bool    `json:"confirm_publication"`
	ExpectedTranslationFingerprint *string `json:"expected_translation_fingerprint,omitempty"`
}

func validTranslationFingerprint(value *string) bool {
	return value == nil || *value == "" || (len(*value) == 64 && commitID.MatchString(*value))
}

// Validate a proposal against the same current revision and protected Markdown
// used for submission, without storing it or approving publication.
func (s *Store) validateTranslation(in translationRequest) (map[string]any, error) {
	if in.ExpectedRevision < 1 || len(in.Text) == 0 || len(in.Text) > maxArticleBytes || !utf8.ValidString(in.Text) || !validTranslationFingerprint(in.ExpectedTranslationFingerprint) {
		return nil, ErrInvalid
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	src, ok := s.db.Sources[in.SourceID]
	if !ok {
		return nil, ErrNotFound
	}
	if src.Kind != "program" || !articlePath(in.Path, src.Kind) {
		return nil, ErrInvalid
	}
	if moveDeletionLocks(s.db, in.SourceID, in.Path) {
		return nil, ErrConflict
	}
	a, ok := s.db.Articles[in.SourceID+"/"+in.Path]
	if !ok || a.Deleted {
		return nil, ErrNotFound
	}
	if len(a.Versions) != in.ExpectedRevision {
		return nil, ErrConflict
	}
	rev := a.Versions[len(a.Versions)-1]
	if in.OriginalSHA != rev.SHA {
		return nil, ErrConflict
	}
	if in.ExpectedTranslationFingerprint != nil && *in.ExpectedTranslationFingerprint != translationFingerprint(a.Translation) {
		return nil, ErrConflict
	}
	for _, conflict := range a.Conflicts {
		if conflict.Resolution == "" {
			return nil, ErrConflict
		}
	}
	return map[string]any{
		"source_id": in.SourceID, "path": in.Path, "expected_revision": in.ExpectedRevision,
		"original_sha256": rev.SHA, "translation_fingerprint": translationFingerprint(a.Translation), "structural_valid": reflect.DeepEqual(protectedMarkdown(rev.Text), protectedMarkdown(in.Text)),
		"russian_prose": containsRussianProse(in.Text), "unchanged": strings.TrimSpace(rev.Text) == strings.TrimSpace(in.Text),
		"credential_like": secretContent.MatchString(in.Text), "saved": false,
		"source_status": s.sourceStatusLocked(in.SourceID, src),
	}, nil
}

// Preserve heading levels, list shape, code and destinations in order. This
// detects some dropped steps but cannot verify prose meaning or order.
func protectedMarkdown(value string) []string {
	source := []byte(value)
	root := markdown.Parser().Parse(text.NewReader(source))
	result := []string{}
	ast.Walk(root, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			if _, ok := node.(*ast.List); ok {
				result = append(result, "list:end")
			}
			if _, ok := node.(*extast.Table); ok {
				result = append(result, "table:end")
			}
			return ast.WalkContinue, nil
		}
		switch n := node.(type) {
		case *ast.Heading:
			result = append(result, fmt.Sprintf("heading:%d", n.Level))
		case *ast.List:
			result = append(result, fmt.Sprintf("list:%t:%d", n.IsOrdered(), n.Start))
		case *ast.ListItem:
			result = append(result, "list:item")
		case *extast.TaskCheckBox:
			result = append(result, fmt.Sprintf("task:%t", n.IsChecked))
		case *extast.Table:
			result = append(result, "table:start")
		case *extast.TableHeader:
			result = append(result, "table:header")
		case *extast.TableRow:
			result = append(result, "table:row")
		case *extast.TableCell:
			result = append(result, "table:cell:"+n.Alignment.String())
		case *ast.FencedCodeBlock:
			info := ""
			if n.Info != nil {
				info = string(n.Info.Segment.Value(source))
			}
			result = append(result, "fence:"+info+"\x00"+string(n.Lines().Value(source)))
		case *ast.CodeBlock:
			result = append(result, "code:"+string(n.Lines().Value(source)))
		case *ast.CodeSpan:
			result = append(result, "inline:"+string(nodeText(n, source)))
		case *ast.Link:
			result = append(result, "link:"+string(n.Destination))
		case *ast.Image:
			result = append(result, "image:"+string(n.Destination))
		case *ast.AutoLink:
			result = append(result, "autolink:"+string(n.URL(source)))
		case *ast.RawHTML:
			result = append(result, "html-inline:"+string(n.Segments.Value(source)))
		case *ast.HTMLBlock:
			value := string(n.Lines().Value(source))
			if n.HasClosure() {
				value += string(n.ClosureLine.Value(source))
			}
			result = append(result, "html-block:"+value)
		}
		return ast.WalkContinue, nil
	})
	return result
}
func (s *Store) submitTranslation(in translationRequest) (Translation, error) {
	return s.saveTranslation(in, false)
}

// Automatic translations of public articles remain private drafts until an
// owner explicitly approves publication through submitTranslation.
func (s *Store) saveTranslation(in translationRequest, privateDraft bool) (Translation, error) {
	if in.ExpectedRevision < 1 || len(in.Text) == 0 || len(in.Text) > maxArticleBytes || !utf8.ValidString(in.Text) || !validTranslationFingerprint(in.ExpectedTranslationFingerprint) {
		return Translation{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	src, ok := s.db.Sources[in.SourceID]
	if !ok {
		return Translation{}, ErrNotFound
	}
	if src.Kind != "program" || !articlePath(in.Path, src.Kind) {
		return Translation{}, ErrInvalid
	}
	key := in.SourceID + "/" + in.Path
	if moveDeletionLocks(s.db, in.SourceID, in.Path) {
		return Translation{}, ErrConflict
	}
	a, ok := s.db.Articles[key]
	if !ok || a.Deleted {
		return Translation{}, ErrNotFound
	}
	if len(a.Versions) != in.ExpectedRevision {
		return Translation{}, ErrConflict
	}
	rev := a.Versions[len(a.Versions)-1]
	if in.OriginalSHA != rev.SHA {
		return Translation{}, ErrConflict
	}
	if in.ExpectedTranslationFingerprint != nil && *in.ExpectedTranslationFingerprint != translationFingerprint(a.Translation) {
		return Translation{}, ErrConflict
	}
	if privateDraft && a.Translation != nil && a.Translation.OriginalSHA == rev.SHA {
		return Translation{}, ErrConflict
	}
	for _, c := range a.Conflicts {
		if c.Resolution == "" {
			return Translation{}, ErrConflict
		}
	}
	if !reflect.DeepEqual(protectedMarkdown(rev.Text), protectedMarkdown(in.Text)) {
		return Translation{}, ErrInvalid
	}
	public := sourcePublic(s.db, src, time.Now())
	if public && ((!in.ConfirmPublication && !privateDraft) || secretContent.MatchString(in.Text)) {
		return Translation{}, ErrInvalid
	}
	origin := ""
	if privateDraft {
		origin = "local_model"
	} else if a.Translation != nil && a.Translation.OriginalSHA == rev.SHA && a.Translation.Text == in.Text {
		// Approval of an unchanged draft does not change its provenance.
		origin = a.Translation.Origin
	}
	tr := Translation{Language: "ru", OriginalSHA: rev.SHA, Text: in.Text, SHA: digest([]byte(in.Text)), Created: time.Now().UTC().Format(time.RFC3339Nano), PublicApproved: public && in.ConfirmPublication, Origin: origin}
	a.Translation = &tr
	db := s.clone()
	if origin != "" && db.Version < 11 {
		db.Version = 11
	}
	db.Articles[key] = a
	reconcileMoves(&db)
	if e := s.commit(db); e != nil {
		return Translation{}, e
	}
	return tr, nil
}
func displayedRevision(a Article, revision Revision, public bool) (Revision, bool) {
	tr := a.Translation
	if tr == nil || tr.OriginalSHA != revision.SHA || (public && !tr.PublicApproved) {
		return revision, false
	}
	return Revision{Number: revision.Number, Text: tr.Text, SHA: tr.SHA, Created: tr.Created}, true
}

type translationCandidate struct {
	SourceID    string `json:"source_id"`
	Path        string `json:"path"`
	Revision    int    `json:"revision"`
	OriginalSHA string `json:"original_sha256"`
	Visibility  string `json:"visibility"`
	State       string `json:"state"`
}

func (s *Store) translationCandidates() []translationCandidate {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []translationCandidate{}
	for _, a := range s.db.Articles {
		src := s.db.Sources[a.SourceID]
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
		if sourcePublic(s.db, src, time.Now()) {
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
		out = append(out, translationCandidate{SourceID: a.SourceID, Path: a.Path, Revision: r.Number, OriginalSHA: r.SHA, Visibility: visibility, State: state})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SourceID != out[j].SourceID {
			return out[i].SourceID < out[j].SourceID
		}
		return out[i].Path < out[j].Path
	})
	return out
}
