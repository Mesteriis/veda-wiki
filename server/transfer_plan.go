package main

import (
	"net/url"
	"path"
	"sort"
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// transferPlan is a read-only snapshot. It is not an authorization or a
// reservation: execution must revalidate every revision and destination.
type transferPlanItem struct {
	SourcePath      string `json:"source_path"`
	DestinationPath string `json:"destination_path"`
	Revision        int    `json:"revision"`
	SHA             string `json:"sha256"`
	Destination     string `json:"destination_status"`
}
type transferPlan struct {
	Articles []transferPlanItem `json:"articles"`
}

func (s *Store) planTransfer(in copyRequest) (transferPlan, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.planTransferLocked(in)
}

// Caller holds the store mutex or read mutex.
func (s *Store) planTransferLocked(in copyRequest) (transferPlan, error) {
	if in.SourceID == in.DestinationID || in.ExpectedRevision < 1 {
		return transferPlan{}, ErrInvalid
	}
	src, ok := s.db.Sources[in.SourceID]
	if !ok {
		return transferPlan{}, ErrNotFound
	}
	dst, ok := s.db.Sources[in.DestinationID]
	if !ok {
		return transferPlan{}, ErrNotFound
	}
	if !articlePath(in.SourcePath, src.Kind) || !articlePath(in.DestinationPath, dst.Kind) {
		return transferPlan{}, ErrInvalid
	}
	root, ok := s.db.Articles[in.SourceID+"/"+in.SourcePath]
	if !ok || root.Deleted {
		return transferPlan{}, ErrNotFound
	}
	if len(root.Versions) != in.ExpectedRevision {
		return transferPlan{}, ErrConflict
	}
	type pair struct{ from, to string }
	pending := []pair{{in.SourcePath, in.DestinationPath}}
	mappedFrom, mappedTo := map[string]string{}, map[string]string{}
	result := transferPlan{Articles: []transferPlanItem{}}
	edges := 0
	for len(pending) > 0 {
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if to, exists := mappedFrom[current.from]; exists {
			if to != current.to {
				return transferPlan{}, ErrConflict
			}
			continue
		}
		if from, exists := mappedTo[current.to]; exists && from != current.from {
			return transferPlan{}, ErrConflict
		}
		if len(mappedFrom) >= 2000 {
			return transferPlan{}, ErrInvalid
		}
		mappedFrom[current.from], mappedTo[current.to] = current.to, current.from
		a, exists := s.db.Articles[in.SourceID+"/"+current.from]
		if !exists || a.Deleted || len(a.Versions) == 0 {
			return transferPlan{}, ErrNotFound
		}
		for _, c := range a.Conflicts {
			if c.Resolution == "" {
				return transferPlan{}, ErrConflict
			}
		}
		rev := a.Versions[len(a.Versions)-1]
		state := "missing"
		if _, exists := s.db.Articles[in.DestinationID+"/"+current.to]; exists {
			state = "conflict"
			if linksPresent(s.db, in.DestinationID, map[string]string{current.to: rev.SHA}) {
				state = "matching"
			}
		}
		result.Articles = append(result.Articles, transferPlanItem{current.from, current.to, len(a.Versions), rev.SHA, state})
		rootNode := markdown.Parser().Parse(text.NewReader([]byte(rev.Text)))
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
				parseErr = ErrInvalid
				return ast.WalkStop, nil
			}
			if u.Scheme != "" || u.Host != "" || u.Path == "" {
				return ast.WalkContinue, nil
			}
			if strings.HasPrefix(u.Path, "/") {
				parseErr = ErrInvalid
				return ast.WalkStop, nil
			}
			from := path.Clean(path.Join(path.Dir(current.from), u.Path))
			to := path.Clean(path.Join(path.Dir(current.to), u.Path))
			if !articlePath(from, src.Kind) || !articlePath(to, dst.Kind) {
				parseErr = ErrInvalid
				return ast.WalkStop, nil
			}
			edges++
			if edges > 10000 {
				parseErr = ErrInvalid
				return ast.WalkStop, nil
			}
			pending = append(pending, pair{from, to})
			return ast.WalkContinue, nil
		})
		if parseErr != nil {
			return transferPlan{}, parseErr
		}
	}
	sort.Slice(result.Articles, func(i, j int) bool { return result.Articles[i].SourcePath < result.Articles[j].SourcePath })
	return result, nil
}
