package transfer

import (
	"net/url"
	"os"
	"strings"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// Dependencies are the relative images and linked articles an article needs
// at its destination.
type Dependencies struct {
	Assets         map[string]domain.BlobRef
	Links          map[string]string
	DirectoryLinks bool
}

// ResolveDependencies resolves relative images and verifies linked articles
// at the destination, reading db and the store's blobs. Embedded HTML and
// links without an identical destination remain blocked. db is not changed.
func (s Service) ResolveDependencies(db *domain.Database, value, sourcePath, targetPath string, src, target domain.Source) (Dependencies, error) {
	document := []byte(value)
	doc := domain.Markdown.Parser().Parse(text.NewReader(document))
	assets := map[string]domain.BlobRef{}
	links := map[string]string{}
	verified := map[domain.BlobRef]bool{}
	total := int64(0)
	var failure error
	directoryLinks := false
	ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		var dest []byte
		image := false
		switch n := node.(type) {
		case *ast.Link:
			dest = n.Destination
		case *ast.Image:
			dest = n.Destination
			image = true
		case *ast.RawHTML, *ast.HTMLBlock:
			failure = domain.ErrInvalid
			return ast.WalkStop, nil
		default:
			return ast.WalkContinue, nil
		}
		parsed, e := url.Parse(string(dest))
		if e != nil {
			failure = domain.ErrInvalid
			return ast.WalkStop, nil
		}
		if parsed.Scheme != "" || parsed.Host != "" || parsed.Path == "" {
			return ast.WalkContinue, nil
		}
		if target.ConnectionID == "" && target.ImportGeneration != "" {
			failure = domain.ErrInvalid
			return ast.WalkStop, nil
		}
		from, fromOK := domain.RepositoryTarget(sourcePath, parsed.Path)
		to, toOK := domain.RepositoryTarget(targetPath, parsed.Path)
		if !image && (!fromOK || !domain.ArticlePath(from, src.Kind)) {
			from, fromOK, _ = DirectoryTransferSource(db, src.ID, sourcePath, parsed.Path)
			to, toOK = domain.DirectoryReadmeTarget(targetPath, parsed.Path)
			directoryLinks = true
		}
		if !fromOK || !toOK || (target.Kind == "program" && !strings.HasPrefix(to, "docs/")) {
			failure = domain.ErrInvalid
			return ast.WalkStop, nil
		}
		if !image {
			if from == sourcePath {
				if to == targetPath {
					return ast.WalkContinue, nil
				}
				failure = domain.ErrInvalid
				return ast.WalkStop, nil
			}
			if !domain.ArticlePath(from, src.Kind) || !domain.ArticlePath(to, target.Kind) || to == targetPath {
				failure = domain.ErrInvalid
				return ast.WalkStop, nil
			}
			original, ok := db.Articles[src.ID+"/"+from]
			if !ok || original.Deleted || len(original.Versions) == 0 {
				failure = domain.ErrInvalid
				return ast.WalkStop, nil
			}
			for _, conflict := range original.Conflicts {
				if conflict.Resolution == "" {
					failure = domain.ErrConflict
					return ast.WalkStop, nil
				}
			}
			sha := original.Versions[len(original.Versions)-1].SHA
			if !domain.LinksPresent(*db, target.ID, map[string]string{to: sha}) {
				failure = domain.ErrConflict
				return ast.WalkStop, nil
			}
			links[to] = sha
			if len(links) > 2000 {
				failure = domain.ErrInvalid
				return ast.WalkStop, nil
			}
			return ast.WalkContinue, nil
		}
		if target.ConnectionID != "" && !domain.RemoteAssetPath(to, target.Kind) {
			failure = domain.ErrInvalid
			return ast.WalkStop, nil
		}
		ref, ok := src.Assets[from]
		if !ok {
			failure = domain.ErrInvalid
			return ast.WalkStop, nil
		}
		if existing, ok := target.Assets[to]; ok && existing != ref {
			failure = domain.ErrConflict
			return ast.WalkStop, nil
		}
		if existing, ok := assets[to]; ok && existing != ref {
			failure = domain.ErrConflict
			return ast.WalkStop, nil
		}
		if !verified[ref] {
			if _, err := domain.HexKey(ref.SHA); err != nil {
				failure = domain.ErrInvalid
				return ast.WalkStop, nil
			}
			info, err := os.Lstat(s.blobPath(ref))
			if err != nil || !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > 4<<20 || total+info.Size() > 16<<20 {
				failure = domain.ErrInvalid
				return ast.WalkStop, nil
			}
			if err := s.Store.VerifyBlob(ref); err != nil {
				failure = err
				return ast.WalkStop, nil
			}
			total += info.Size()
			verified[ref] = true
		}
		assets[to] = ref
		if len(assets) > 2000 {
			failure = domain.ErrInvalid
			return ast.WalkStop, nil
		}
		return ast.WalkContinue, nil
	})
	return Dependencies{Assets: assets, Links: links, DirectoryLinks: directoryLinks}, failure
}

// blobPath is the file of an image blob, for size checks before reading it.
func (s Service) blobPath(ref domain.BlobRef) string {
	return s.Store.BlobPath(ref.SHA)
}
