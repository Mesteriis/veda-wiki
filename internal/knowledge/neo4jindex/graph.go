package neo4jindex

import (
	"context"
	"slices"
	"sort"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"

	"github.com/Mesteriis/veda-wiki/internal/knowledge"
)

// Graph implements knowledge.Index with memindex's semantics: breadth-first
// from the center over mention edges (document to entity) and claim edges
// (entity to entity) of allowed evidence, honouring the source and relation
// filters, up to the depth and node cap; then every edge between included
// nodes up to the edge cap. Each hop reads only claims of allowed documents
// that touch the frontier; mention edges come from the allowed aggregates.
func (ix *Index) Graph(ctx context.Context, allowed knowledge.AllowedSet, fp knowledge.Fingerprint, q knowledge.GraphQuery) (knowledge.Subgraph, error) {
	var out knowledge.Subgraph
	err := ix.withView(ctx, allowed, fp, func(tx neo4j.ManagedTransaction, v *view) error {
		g, err := v.graph(ctx, tx, q)
		out = g
		return err
	})
	if err != nil {
		return knowledge.Subgraph{}, err
	}
	return out, nil
}

func (v *view) graph(ctx context.Context, tx neo4j.ManagedTransaction, q knowledge.GraphQuery) (knowledge.Subgraph, error) {
	limits := knowledge.SchemaV1().Limits
	depth := q.Depth
	if depth <= 0 {
		depth = limits.DepthDefault
	}
	depth = min(depth, limits.DepthMax)
	maxNodes := capTo(q.MaxNodes, limits.NodesMax)
	maxEdges := capTo(q.MaxEdges, limits.EdgesMax)
	var center string
	switch {
	case q.Center.EntityID != "" && v.entities[q.Center.EntityID] != nil:
		center = q.Center.EntityID
	case q.Center.EntityID == "" && q.Center.Snapshot != nil && v.published[*q.Center.Snapshot] && sourceOK(q.SourceIDs, *q.Center.Snapshot):
		center = knowledge.DocumentNodeID(*q.Center.Snapshot)
	default:
		return knowledge.Subgraph{}, knowledge.ErrNotFound
	}
	docs := map[string]knowledge.SnapshotKey{}
	var srcDocs []knowledge.SnapshotKey
	for _, k := range v.docs {
		docs[knowledge.DocumentNodeID(k)] = k
		if sourceOK(q.SourceIDs, k) {
			srcDocs = append(srcDocs, k)
		}
	}
	out := knowledge.Subgraph{Nodes: []knowledge.Node{}, Edges: []knowledge.Edge{}}
	included := map[string]bool{center: true}
	frontier := []string{center}
	for d := 0; d < depth && len(frontier) > 0; d++ {
		edges, err := v.adjacent(ctx, tx, q, docs, srcDocs, frontier)
		if err != nil {
			return out, err
		}
		adjacent := map[string][]int{}
		for i, e := range edges {
			adjacent[e.From] = append(adjacent[e.From], i)
			adjacent[e.To] = append(adjacent[e.To], i)
		}
		var next []string
		for _, id := range frontier {
			for _, i := range adjacent[id] {
				other := edges[i].To
				if other == id {
					other = edges[i].From
				}
				// A claim end without allowed mentions is not a node of
				// this view: it is neither included nor traversed.
				if _, doc := docs[other]; !doc && v.entities[other] == nil || included[other] {
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
		if k, ok := docs[id]; ok {
			key := k
			out.Nodes = append(out.Nodes, knowledge.Node{ID: id, Kind: knowledge.NodeDocument, Label: k.Path, Key: &key})
			continue
		}
		e := v.entities[id]
		out.Nodes = append(out.Nodes, knowledge.Node{ID: id, Kind: knowledge.NodeEntity, Type: e.typ, Label: e.name()})
	}
	edges, err := v.between(ctx, tx, q, docs, srcDocs, included)
	if err != nil {
		return out, err
	}
	for _, e := range edges {
		if len(out.Edges) == maxEdges {
			out.Truncated = true
			break
		}
		out.Edges = append(out.Edges, e)
	}
	return out, nil
}

// adjacent lists the edges (without evidence) touching frontier, sorted by
// ID: mention edges from the aggregates, claim edges from allowed claims.
func (v *view) adjacent(ctx context.Context, tx neo4j.ManagedTransaction, q knowledge.GraphQuery, docs map[string]knowledge.SnapshotKey, srcDocs []knowledge.SnapshotKey, frontier []string) ([]knowledge.Edge, error) {
	seen := map[string]bool{}
	var out []knowledge.Edge
	add := func(e knowledge.Edge) {
		if !seen[e.ID] {
			seen[e.ID] = true
			out = append(out, e)
		}
	}
	var entities []string
	for _, id := range frontier {
		if k, ok := docs[id]; ok {
			if sourceOK(q.SourceIDs, k) {
				for _, eid := range sortedIDs(v.docEntities[k]) {
					add(mentionEdge(k, eid))
				}
			}
			continue
		}
		entities = append(entities, id)
		for _, k := range srcDocs {
			if e := v.entities[id]; e != nil && e.docs[k] > 0 {
				add(mentionEdge(k, id))
			}
		}
	}
	claims, err := v.claims(ctx, tx, srcDocs, entities, unlimited)
	if err != nil {
		return nil, err
	}
	for _, c := range claims {
		if relationOK(q.Relations, c.Relation) {
			add(claimEdge(c))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// between lists every edge with both ends in included, with evidence and
// counts, sorted by ID.
func (v *view) between(ctx context.Context, tx neo4j.ManagedTransaction, q knowledge.GraphQuery, docs map[string]knowledge.SnapshotKey, srcDocs []knowledge.SnapshotKey, included map[string]bool) ([]knowledge.Edge, error) {
	var out []knowledge.Edge
	var entities []string
	evDocs := map[knowledge.SnapshotKey]bool{}
	evEntities := map[string]bool{}
	for _, id := range sortedIDs(included) {
		if _, ok := docs[id]; !ok {
			entities = append(entities, id)
		}
	}
	for _, k := range srcDocs {
		if !included[knowledge.DocumentNodeID(k)] {
			continue
		}
		for _, eid := range entities {
			if ent := v.entities[eid]; ent != nil && ent.docs[k] > 0 {
				e := mentionEdge(k, eid)
				e.Count = ent.docs[k]
				out = append(out, e)
				evDocs[k], evEntities[eid] = true, true
			}
		}
	}
	quotes, err := v.quotes(ctx, tx, evDocs, evEntities)
	if err != nil {
		return nil, err
	}
	for i := range out {
		k := docs[out[i].From]
		out[i].Evidence = append([]knowledge.Evidence{}, quotes[k][out[i].To]...)
	}
	claims, err := v.claims(ctx, tx, srcDocs, entities, unlimited)
	if err != nil {
		return nil, err
	}
	for _, c := range claims {
		if included[c.HeadID] && included[c.TailID] && relationOK(q.Relations, c.Relation) {
			out = append(out, claimEdge(c))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func mentionEdge(k knowledge.SnapshotKey, entityID string) knowledge.Edge {
	doc := knowledge.DocumentNodeID(k)
	return knowledge.Edge{ID: "men-" + doc[4:] + "-" + entityID[4:], Kind: knowledge.EdgeMention, From: doc, To: entityID, Evidence: []knowledge.Evidence{}}
}

func claimEdge(c knowledge.Claim) knowledge.Edge {
	return knowledge.Edge{ID: c.ID, Kind: knowledge.EdgeRelation, From: c.HeadID, To: c.TailID, Relation: c.Relation, Status: c.Status, Markers: c.Markers, Evidence: []knowledge.Evidence{c.Evidence}}
}

func relationOK(relations []string, relation string) bool {
	return len(relations) == 0 || slices.Contains(relations, relation)
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
