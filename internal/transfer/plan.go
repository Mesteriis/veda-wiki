package transfer

import (
	"net/url"
	"path"
	"sort"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// PlanItem is one article of a transfer plan.
type PlanItem struct {
	SourcePath      string `json:"source_path"`
	DestinationPath string `json:"destination_path"`
	Revision        int    `json:"revision"`
	SHA             string `json:"sha256"`
	Destination     string `json:"destination_status"`
	DirectoryIndex  bool   `json:"directory_index,omitempty"`
	CredentialLike  bool   `json:"credential_like"`
}

// Plan is a read-only snapshot. It is not an authorization or a
// reservation: execution must revalidate every revision and destination.
// The statuses are those of Service.Status.
type Plan struct {
	Articles          []PlanItem           `json:"articles"`
	SourceStatus      *domain.SourceStatus `json:"source_status"`
	DestinationStatus *domain.SourceStatus `json:"destination_status"`
}

// DirectoryTransferSource resolves a directory link of the article at
// articlePath to the directory's README.md or index.md in sourceID. README
// wins, matching reader resolution. It reports the article, whether the link
// is valid, and whether it resolved to index.md.
func DirectoryTransferSource(db *domain.Database, sourceID, articlePath, destination string) (string, bool, bool) {
	readme, ok := domain.DirectoryReadmeTarget(articlePath, destination)
	if !ok {
		return "", false, false
	}
	if a, exists := db.Articles[sourceID+"/"+readme]; exists && !a.Deleted && len(a.Versions) > 0 {
		return readme, true, false
	}
	index := path.Join(path.Dir(readme), "index.md")
	if a, exists := db.Articles[sourceID+"/"+index]; exists && !a.Deleted && len(a.Versions) > 0 {
		return index, true, true
	}
	return readme, true, false
}

// PlanTransfer plans the linked graph of a copy from one snapshot, with
// both sources' statuses. It only reads.
func (s Service) PlanTransfer(in CopyRequest) (Plan, error) {
	var plan Plan
	err := s.Store.View(func(db *domain.Database) error {
		var err error
		if plan, err = BuildPlan(db, in); err != nil {
			return err
		}
		if s.Status != nil {
			source := s.Status(*db, in.SourceID, db.Sources[in.SourceID])
			destination := s.Status(*db, in.DestinationID, db.Sources[in.DestinationID])
			plan.SourceStatus, plan.DestinationStatus = &source, &destination
		}
		return nil
	})
	if err != nil {
		return Plan{}, err
	}
	return plan, nil
}

// BuildPlan plans the linked graph of a copy over db without source
// statuses. db is not changed.
func BuildPlan(db *domain.Database, in CopyRequest) (Plan, error) {
	if in.SourceID == in.DestinationID || in.ExpectedRevision < 1 {
		return Plan{}, domain.ErrInvalid
	}
	src, ok := db.Sources[in.SourceID]
	if !ok {
		return Plan{}, domain.ErrNotFound
	}
	dst, ok := db.Sources[in.DestinationID]
	if !ok {
		return Plan{}, domain.ErrNotFound
	}
	if !domain.ArticlePath(in.SourcePath, src.Kind) || !domain.ArticlePath(in.DestinationPath, dst.Kind) {
		return Plan{}, domain.ErrInvalid
	}
	root, ok := db.Articles[in.SourceID+"/"+in.SourcePath]
	if !ok || root.Deleted {
		return Plan{}, domain.ErrNotFound
	}
	if len(root.Versions) != in.ExpectedRevision {
		return Plan{}, domain.ErrConflict
	}
	type pair struct {
		from, to string
		index    bool
	}
	pending := []pair{{from: in.SourcePath, to: in.DestinationPath}}
	mappedFrom, mappedTo := map[string]string{}, map[string]string{}
	indexMapped := map[string]bool{}
	result := Plan{Articles: []PlanItem{}}
	edges := 0
	for len(pending) > 0 {
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		indexMapped[current.from] = indexMapped[current.from] || current.index
		if to, exists := mappedFrom[current.from]; exists {
			if to != current.to {
				return Plan{}, domain.ErrConflict
			}
			continue
		}
		if from, exists := mappedTo[current.to]; exists && from != current.from {
			return Plan{}, domain.ErrConflict
		}
		if len(mappedFrom) >= 2000 {
			return Plan{}, domain.ErrInvalid
		}
		mappedFrom[current.from], mappedTo[current.to] = current.to, current.from
		a, exists := db.Articles[in.SourceID+"/"+current.from]
		if !exists || a.Deleted || len(a.Versions) == 0 {
			return Plan{}, domain.ErrNotFound
		}
		for _, c := range a.Conflicts {
			if c.Resolution == "" {
				return Plan{}, domain.ErrConflict
			}
		}
		rev := a.Versions[len(a.Versions)-1]
		state := "missing"
		if _, exists := db.Articles[in.DestinationID+"/"+current.to]; exists {
			state = "conflict"
			if domain.LinksPresent(*db, in.DestinationID, map[string]string{current.to: rev.SHA}) {
				state = "matching"
			}
		}
		result.Articles = append(result.Articles, PlanItem{SourcePath: current.from, DestinationPath: current.to, Revision: len(a.Versions), SHA: rev.SHA, Destination: state, CredentialLike: domain.CredentialPattern.MatchString(rev.Text)})
		rootNode := domain.Markdown.Parser().Parse(text.NewReader([]byte(rev.Text)))
		var parseErr error
		ast.Walk(rootNode, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
			if !entering {
				return ast.WalkContinue, nil
			}
			if _, ok := node.(*ast.Image); ok {
				return ast.WalkSkipChildren, nil
			}
			link, ok := node.(*ast.Link)
			if !ok {
				return ast.WalkContinue, nil
			}
			u, err := url.Parse(string(link.Destination))
			if err != nil {
				parseErr = domain.ErrInvalid
				return ast.WalkStop, nil
			}
			if u.Scheme != "" || u.Host != "" || u.Path == "" {
				return ast.WalkContinue, nil
			}
			from, fromOK := domain.RepositoryTarget(current.from, u.Path)
			to, toOK := domain.RepositoryTarget(current.to, u.Path)
			index := false
			if !fromOK || !domain.ArticlePath(from, src.Kind) {
				from, fromOK, index = DirectoryTransferSource(db, src.ID, current.from, u.Path)
				to, toOK = domain.DirectoryReadmeTarget(current.to, u.Path)
			}
			if !fromOK || !toOK || !domain.ArticlePath(from, src.Kind) || !domain.ArticlePath(to, dst.Kind) {
				parseErr = domain.ErrInvalid
				return ast.WalkStop, nil
			}
			edges++
			if edges > 10000 {
				parseErr = domain.ErrInvalid
				return ast.WalkStop, nil
			}
			pending = append(pending, pair{from: from, to: to, index: index})
			return ast.WalkContinue, nil
		})
		if parseErr != nil {
			return Plan{}, parseErr
		}
	}
	for i := range result.Articles {
		item := &result.Articles[i]
		item.DirectoryIndex = indexMapped[item.SourcePath]
		if !item.DirectoryIndex || item.Destination == "matching" {
			continue
		}
		competitor := path.Join(path.Dir(item.DestinationPath), "index.md")
		if _, mapped := mappedTo[competitor]; mapped {
			return Plan{}, domain.ErrConflict
		}
		if a, exists := db.Articles[dst.ID+"/"+competitor]; exists && !a.Deleted {
			return Plan{}, domain.ErrConflict
		}
	}
	sort.Slice(result.Articles, func(i, j int) bool { return result.Articles[i].SourcePath < result.Articles[j].SourcePath })
	return result, nil
}
