// Package translation validates and stores Russian translations of program
// documentation, lists the articles that still need one, and runs the
// optional local translation worker.
package translation

import (
	"fmt"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/yuin/goldmark/ast"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/render"
	"github.com/Mesteriis/veda-wiki/internal/store"
)

// Request is a translation proposal for the current revision of an article.
type Request struct {
	SourceID                       string  `json:"source_id"`
	Path                           string  `json:"path"`
	ExpectedRevision               int     `json:"expected_revision"`
	OriginalSHA                    string  `json:"original_sha256"`
	Text                           string  `json:"text"`
	ConfirmPublication             bool    `json:"confirm_publication"`
	ExpectedTranslationFingerprint *string `json:"expected_translation_fingerprint,omitempty"`
}

// Service runs translation operations against Store. Status, when set,
// reports a source's status in validation results; it runs under the store
// read lock.
type Service struct {
	Store  *store.Store
	Status func(db domain.Database, sourceID string, src domain.Source) domain.SourceStatus
}

func validTranslationFingerprint(value *string) bool {
	return value == nil || *value == "" || (len(*value) == 64 && domain.ValidCommitID(*value))
}

func (in Request) valid() bool {
	return in.ExpectedRevision >= 1 && len(in.Text) != 0 && len(in.Text) <= domain.MaxArticleBytes && utf8.ValidString(in.Text) && validTranslationFingerprint(in.ExpectedTranslationFingerprint)
}

// Validate checks a proposal against the same current revision and protected
// Markdown used for submission, without storing it or approving publication.
// It only reads the store.
func (s Service) Validate(in Request) (map[string]any, error) {
	if !in.valid() {
		return nil, domain.ErrInvalid
	}
	var result map[string]any
	err := s.Store.View(func(db *domain.Database) error {
		src, ok := db.Sources[in.SourceID]
		if !ok {
			return domain.ErrNotFound
		}
		if src.Kind != "program" || !domain.ArticlePath(in.Path, src.Kind) {
			return domain.ErrInvalid
		}
		if domain.MoveDeletionLocks(*db, in.SourceID, in.Path) {
			return domain.ErrConflict
		}
		a, ok := db.Articles[in.SourceID+"/"+in.Path]
		if !ok || a.Deleted {
			return domain.ErrNotFound
		}
		if domain.LatestRevision(a) != in.ExpectedRevision {
			return domain.ErrConflict
		}
		rev, _ := domain.LastRevision(a)
		if in.OriginalSHA != rev.SHA {
			return domain.ErrConflict
		}
		if in.ExpectedTranslationFingerprint != nil && *in.ExpectedTranslationFingerprint != domain.TranslationFingerprint(a.Translation) {
			return domain.ErrConflict
		}
		for _, conflict := range a.Conflicts {
			if conflict.Resolution == "" {
				return domain.ErrConflict
			}
		}
		var status any
		if s.Status != nil {
			status = s.Status(*db, in.SourceID, src)
		}
		result = map[string]any{
			"source_id": in.SourceID, "path": in.Path, "expected_revision": in.ExpectedRevision,
			"original_sha256": rev.SHA, "translation_fingerprint": domain.TranslationFingerprint(a.Translation), "structural_valid": reflect.DeepEqual(protectedMarkdown(rev.Text), protectedMarkdown(in.Text)),
			"russian_prose": containsRussianProse(in.Text), "unchanged": strings.TrimSpace(rev.Text) == strings.TrimSpace(in.Text),
			"credential_like": domain.CredentialLike(in.Text), "saved": false,
			"source_status": status,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// Preserve heading levels, list shape, code and destinations in order. This
// detects some dropped steps but cannot verify prose meaning or order.
func protectedMarkdown(value string) []string {
	source := []byte(value)
	root := domain.Markdown.Parser().Parse(text.NewReader(source))
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
			result = append(result, "inline:"+string(render.NodeText(n, source)))
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
