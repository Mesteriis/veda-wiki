package memindex

import (
	"context"
	"slices"
	"sort"

	"github.com/Mesteriis/veda-wiki/internal/knowledge"
)

// Graph implements knowledge.Index: breadth-first from the center over
// mention edges (document to entity) and claim edges (entity to entity) of
// the view, honouring the source and relation filters, up to the depth and
// node cap; then every edge between included nodes up to the edge cap.
func (ix *Index) Graph(ctx context.Context, allowed knowledge.AllowedSet, fp knowledge.Fingerprint, q knowledge.GraphQuery) (knowledge.Subgraph, error) {
	v, err := ix.view(ctx, allowed, fp)
	if err != nil {
		return knowledge.Subgraph{}, err
	}
	limits := knowledge.SchemaV1().Limits
	depth := q.Depth
	if depth <= 0 {
		depth = limits.DepthDefault
	}
	depth = min(depth, limits.DepthMax)
	maxNodes := capTo(q.MaxNodes, limits.NodesMax)
	maxEdges := capTo(q.MaxEdges, limits.EdgesMax)
	edges := v.edges(q.SourceIDs, q.Relations)
	adjacent := map[string][]int{}
	for i, e := range edges {
		adjacent[e.From] = append(adjacent[e.From], i)
		adjacent[e.To] = append(adjacent[e.To], i)
	}
	var center string
	switch {
	case q.Center.EntityID != "" && v.entities[q.Center.EntityID] != nil:
		center = q.Center.EntityID
	case q.Center.EntityID == "" && q.Center.Snapshot != nil && v.published[*q.Center.Snapshot] && sourceOK(q.SourceIDs, *q.Center.Snapshot):
		center = knowledge.DocumentNodeID(*q.Center.Snapshot)
	default:
		return knowledge.Subgraph{}, knowledge.ErrNotFound
	}
	nodes := v.nodes()
	out := knowledge.Subgraph{Nodes: []knowledge.Node{}, Edges: []knowledge.Edge{}}
	included := map[string]bool{center: true}
	frontier := []string{center}
	for d := 0; d < depth && len(frontier) > 0; d++ {
		var next []string
		for _, id := range frontier {
			for _, i := range adjacent[id] {
				other := edges[i].To
				if other == id {
					other = edges[i].From
				}
				if included[other] {
					continue
				}
				if len(included) == maxNodes {
					out.Truncated = true
					continue
				}
				included[other] = true
				next = append(next, other)
			}
		}
		sort.Strings(next)
		frontier = next
	}
	for _, id := range sortedIDs(included) {
		out.Nodes = append(out.Nodes, nodes[id])
	}
	for _, e := range edges {
		if !included[e.From] || !included[e.To] {
			continue
		}
		if len(out.Edges) == maxEdges {
			out.Truncated = true
			break
		}
		out.Edges = append(out.Edges, e)
	}
	return out, nil
}

func capTo(n, limit int) int {
	if n <= 0 || n > limit {
		return limit
	}
	return n
}

func sourceOK(sources []string, k knowledge.SnapshotKey) bool {
	return len(sources) == 0 || slices.Contains(sources, k.SourceID)
}

// nodes are every entity and document node of the view.
func (v *view) nodes() map[string]knowledge.Node {
	out := map[string]knowledge.Node{}
	for _, e := range v.entities {
		out[e.id] = knowledge.Node{ID: e.id, Kind: knowledge.NodeEntity, Type: e.typ, Label: e.name()}
	}
	for _, k := range v.docs {
		key := k
		out[knowledge.DocumentNodeID(k)] = knowledge.Node{ID: knowledge.DocumentNodeID(k), Kind: knowledge.NodeDocument, Label: k.Path, Key: &key}
	}
	return out
}

// edges are the mention edges (one per document and entity, with up to
// MaxEvidencePerReason quotes) and one edge per claim, sorted by ID.
func (v *view) edges(sources, relations []string) []knowledge.Edge {
	var out []knowledge.Edge
	byPair := map[string]int{}
	for _, m := range v.mentions {
		k := m.Evidence.Key()
		if !sourceOK(sources, k) {
			continue
		}
		doc := knowledge.DocumentNodeID(k)
		id := "men-" + doc[4:] + "-" + m.EntityID[4:]
		i, ok := byPair[id]
		if !ok {
			i = len(out)
			byPair[id] = i
			out = append(out, knowledge.Edge{ID: id, Kind: knowledge.EdgeMention, From: doc, To: m.EntityID, Evidence: []knowledge.Evidence{}})
		}
		out[i].Count++
		if len(out[i].Evidence) < knowledge.MaxEvidencePerReason {
			out[i].Evidence = append(out[i].Evidence, m.Evidence)
		}
	}
	for _, c := range v.claims {
		if !sourceOK(sources, c.Evidence.Key()) || len(relations) > 0 && !slices.Contains(relations, c.Relation) {
			continue
		}
		out = append(out, knowledge.Edge{ID: c.ID, Kind: knowledge.EdgeRelation, From: c.HeadID, To: c.TailID, Relation: c.Relation, Status: c.Status, Markers: c.Markers, Evidence: []knowledge.Evidence{c.Evidence}})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
