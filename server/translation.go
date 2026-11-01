package main

import (
	"reflect"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

type Translation struct {
	Language       string `json:"language"`
	OriginalSHA    string `json:"original_sha256"`
	Text           string `json:"text"`
	SHA            string `json:"sha256"`
	Created        string `json:"created_at"`
	PublicApproved bool   `json:"public_approved"`
}
type translationRequest struct {
	SourceID           string `json:"source_id"`
	Path               string `json:"path"`
	ExpectedRevision   int    `json:"expected_revision"`
	OriginalSHA        string `json:"original_sha256"`
	Text               string `json:"text"`
	ConfirmPublication bool   `json:"confirm_publication"`
}

// Translation cannot alter code or destinations. Preserve their order too, so
// translated prose cannot silently associate a command or link with another step.
func protectedMarkdown(value string) []string {
	source := []byte(value)
	root := markdown.Parser().Parse(text.NewReader(source))
	result := []string{}
	ast.Walk(root, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := node.(type) {
		case *ast.FencedCodeBlock:
			info := ""
			if n.Info != nil {
				info = string(n.Info.Segment.Value(source))
			}
			result = append(result, "fence:"+info+"\x00"+string(n.Lines().Value(source)))
		case *ast.CodeBlock:
			result = append(result, "code:"+string(n.Lines().Value(source)))
		case *ast.CodeSpan:
			result = append(result, "inline:"+string(n.Text(source)))
		case *ast.Link:
			result = append(result, "link:"+string(n.Destination))
		case *ast.Image:
			result = append(result, "image:"+string(n.Destination))
		case *ast.AutoLink:
			result = append(result, "autolink:"+string(n.URL(source)))
		}
		return ast.WalkContinue, nil
	})
	return result
}
func (s *Store) submitTranslation(in translationRequest) (Translation, error) {
	if in.ExpectedRevision < 1 || len(in.Text) == 0 || len(in.Text) > 256000 || !utf8.ValidString(in.Text) {
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
	for _, c := range a.Conflicts {
		if c.Resolution == "" {
			return Translation{}, ErrConflict
		}
	}
	if !reflect.DeepEqual(protectedMarkdown(rev.Text), protectedMarkdown(in.Text)) {
		return Translation{}, ErrInvalid
	}
	public := sourcePublic(s.db, src, time.Now())
	if public && (!in.ConfirmPublication || secretContent.MatchString(in.Text)) {
		return Translation{}, ErrInvalid
	}
	tr := Translation{Language: "ru", OriginalSHA: rev.SHA, Text: in.Text, SHA: digest([]byte(in.Text)), Created: time.Now().UTC().Format(time.RFC3339Nano), PublicApproved: public && in.ConfirmPublication}
	a.Translation = &tr
	db := s.clone()
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
		if blocked {
			continue
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
