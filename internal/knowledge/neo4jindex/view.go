package neo4jindex

import (
	"context"
	"sort"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"

	"github.com/Mesteriis/veda-wiki/internal/knowledge"
)

// allowedDocs lists the complete snapshots of the allowed set.
const allowedDocs = `
UNWIND $allowed AS sid
MATCH (s:Snapshot {id: sid})
WHERE s.complete = true
RETURN s.id, s.source_id, s.path, s.sha`

// entityStats aggregates, per entity, only the mentions of allowed complete
// snapshots: the surface form and snapshot of every mention in Go's
// evidence order, and the resolution of the first one. $allowed is sorted
// in Go key order and the order is its list position plus the
// publish-time evidence rank: no string is ordered in Cypher.
const entityStats = `
UNWIND range(0, size($allowed) - 1) AS di
WITH di, $allowed[di] AS sid
MATCH (s:Snapshot {id: sid})
WHERE s.complete = true
MATCH (s)-[:HAS]->(ev:Evidence)<-[:AT]-(m:Mention)-[:OF]->(e:Entity)
WITH e, s, ev, m, di
ORDER BY di, ev.rank
WITH e, collect([m.surface, s.id]) AS ms, head(collect([m.res_method, m.res_basis])) AS res
RETURN e.id, e.type, e.key, ms, res`

// view is what one request may see, as aggregates over allowed evidence:
// the allowed published documents and, per entity, its names, documents
// and mention counts. Evidence itself is fetched per read (fetch.go), always
// starting from the same allowed snapshots.
type view struct {
	docs        []knowledge.SnapshotKey
	published   map[knowledge.SnapshotKey]bool
	ids         map[knowledge.SnapshotKey]string
	keys        map[string]knowledge.SnapshotKey
	entities    map[string]*entity
	docEntities map[knowledge.SnapshotKey]map[string]bool
}

// entity aggregates one canonical entity over allowed mentions only.
type entity struct {
	id, typ, key string
	names        map[string]int
	resolution   knowledge.Resolution
	mentions     int
	docs         map[knowledge.SnapshotKey]int
}

// load reads the view of allowed under fp inside tx.
func load(ctx context.Context, tx neo4j.ManagedTransaction, allowed knowledge.AllowedSet, fp knowledge.Fingerprint) (*view, error) {
	v := &view{published: map[knowledge.SnapshotKey]bool{}, ids: map[knowledge.SnapshotKey]string{}, keys: map[string]knowledge.SnapshotKey{},
		entities: map[string]*entity{}, docEntities: map[knowledge.SnapshotKey]map[string]bool{}}
	params := map[string]any{"allowed": allowedIDs(allowed, fp)}
	recs, err := collect(ctx, tx, allowedDocs, params)
	if err != nil {
		return nil, err
	}
	for _, r := range recs {
		k := knowledge.SnapshotKey{SourceID: str(r.Values[1]), Path: str(r.Values[2]), SHA: str(r.Values[3])}
		v.docs = append(v.docs, k)
		v.published[k] = true
		v.ids[k] = str(r.Values[0])
		v.keys[str(r.Values[0])] = k
	}
	sort.Slice(v.docs, func(i, j int) bool { return keyLess(v.docs[i], v.docs[j]) })
	if recs, err = collect(ctx, tx, entityStats, params); err != nil {
		return nil, err
	}
	for _, r := range recs {
		e := &entity{id: str(r.Values[0]), typ: str(r.Values[1]), key: str(r.Values[2]), names: map[string]int{}, docs: map[knowledge.SnapshotKey]int{}}
		if res, _ := r.Values[4].([]any); len(res) == 2 {
			e.resolution = knowledge.Resolution{Method: knowledge.ResolutionMethod(str(res[0])), Basis: str(res[1])}
		}
		ms, _ := r.Values[3].([]any)
		for _, item := range ms {
			pair, _ := item.([]any)
			if len(pair) != 2 {
				continue
			}
			k, ok := v.keys[str(pair[1])]
			if !ok {
				continue
			}
			e.names[str(pair[0])]++
			e.mentions++
			e.docs[k]++
			if v.docEntities[k] == nil {
				v.docEntities[k] = map[string]bool{}
			}
			v.docEntities[k][e.id] = true
		}
		if e.mentions > 0 {
			v.entities[e.id] = e
		}
	}
	return v, nil
}

// withView loads the view and runs fn on it in one read transaction.
func (ix *Index) withView(ctx context.Context, allowed knowledge.AllowedSet, fp knowledge.Fingerprint, fn func(tx neo4j.ManagedTransaction, v *view) error) error {
	if err := ix.ensureSchema(ctx); err != nil {
		return err
	}
	return ix.read(ctx, func(tx neo4j.ManagedTransaction) error {
		v, err := load(ctx, tx, allowed, fp)
		if err != nil {
			return err
		}
		return fn(tx, v)
	})
}

// docIDs are the snapshot IDs of keys, sorted in Go key order: queries
// order by position in this list.
func (v *view) docIDs(keys []knowledge.SnapshotKey) []string {
	sorted := append([]knowledge.SnapshotKey(nil), keys...)
	sort.Slice(sorted, func(i, j int) bool { return keyLess(sorted[i], sorted[j]) })
	out := make([]string, 0, len(sorted))
	for _, k := range sorted {
		out = append(out, v.ids[k])
	}
	return out
}

// name is the most frequent allowed surface form; ties go to the smallest.
func (e *entity) name() string {
	best, n := "", -1
	for text, c := range e.names {
		if c > n || c == n && text < best {
			best, n = text, c
		}
	}
	return best
}

// aliases are the other allowed surface forms, sorted.
func (e *entity) aliases() []string {
	name := e.name()
	var out []string
	for text := range e.names {
		if text != name {
			out = append(out, text)
		}
	}
	sort.Strings(out)
	return out
}

func (v *view) summary(e *entity) knowledge.EntitySummary {
	return knowledge.EntitySummary{
		Entity:    knowledge.Entity{ID: e.id, Type: e.typ, Name: e.name(), Key: e.key, Aliases: e.aliases(), Resolution: e.resolution},
		Mentions:  e.mentions,
		Documents: len(e.docs),
	}
}

// generic reports common vocabulary: an entity in more than GenericShare of
// the allowed documents and in at least GenericMinDocs of them.
func (v *view) generic(e *entity) bool {
	return len(e.docs) >= knowledge.GenericMinDocs && float64(len(e.docs)) > knowledge.GenericShare*float64(len(v.docs))
}

// sortedEntities lists entities by allowed documents, mentions, name, ID.
func (v *view) sortedEntities(filter func(*entity) bool) []*entity {
	var out []*entity
	for _, e := range v.entities {
		if filter == nil || filter(e) {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if len(a.docs) != len(b.docs) {
			return len(a.docs) > len(b.docs)
		}
		if a.mentions != b.mentions {
			return a.mentions > b.mentions
		}
		if a.name() != b.name() {
			return a.name() < b.name()
		}
		return a.id < b.id
	})
	return out
}

// normalizedNames are the normalized keys of the allowed surface forms.
func (e *entity) normalizedNames() map[string]bool {
	out := map[string]bool{}
	for text := range e.names {
		if k, _ := knowledge.Normalize(e.typ, text); k != "" {
			out[k] = true
		}
	}
	return out
}

// candidates are other allowed entities of the same type, each scoped to
// its own source by name, that share a normalized allowed name with e.
func (v *view) candidates(e *entity) []knowledge.Candidate {
	if e.resolution.Method != knowledge.ResolveSameSourceName {
		return []knowledge.Candidate{}
	}
	mine := e.normalizedNames()
	out := []knowledge.Candidate{}
	for _, o := range v.sortedEntities(nil) {
		if o.id == e.id || o.typ != e.typ || o.resolution.Method != knowledge.ResolveSameSourceName {
			continue
		}
		var shared []string
		for k := range o.normalizedNames() {
			if mine[k] {
				shared = append(shared, k)
			}
		}
		if len(shared) > 0 {
			sort.Strings(shared)
			out = append(out, knowledge.Candidate{EntityID: o.id, Name: o.name(), Resolution: knowledge.Resolution{Method: knowledge.ResolveCandidate, Basis: shared[0]}})
		}
	}
	return out
}

// docsOf lists the allowed documents of e in key order, at most n.
func (v *view) docsOf(e *entity, n int) []knowledge.SnapshotKey {
	var out []knowledge.SnapshotKey
	for _, k := range v.docs {
		if e.docs[k] > 0 && len(out) < n {
			out = append(out, k)
		}
	}
	return out
}

func sameArticle(a, b knowledge.SnapshotKey) bool {
	return a.SourceID == b.SourceID && a.Path == b.Path
}

func keyLess(a, b knowledge.SnapshotKey) bool {
	if a.SourceID != b.SourceID {
		return a.SourceID < b.SourceID
	}
	if a.Path != b.Path {
		return a.Path < b.Path
	}
	return a.SHA < b.SHA
}

func evidenceLess(a, b knowledge.Evidence) bool {
	if a.Key() != b.Key() {
		return keyLess(a.Key(), b.Key())
	}
	if a.BlockID != b.BlockID {
		return a.BlockID < b.BlockID
	}
	if a.Start != b.Start {
		return a.Start < b.Start
	}
	return a.End < b.End
}

func sortedIDs(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
