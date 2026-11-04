package memindex

import (
	"context"
	"sort"
	"strings"

	"github.com/Mesteriis/veda-wiki/internal/knowledge"
)

// view is the part of the index one request may see: only snapshots in the
// allowed set under the active fingerprint. Every read is computed from it.
type view struct {
	docs        []knowledge.SnapshotKey
	published   map[knowledge.SnapshotKey]bool
	mentions    []knowledge.Mention
	claims      []knowledge.Claim
	entities    map[string]*entity
	docEntities map[knowledge.SnapshotKey]map[string]bool
}

// entity aggregates one canonical entity over allowed mentions only.
type entity struct {
	id, typ, key string
	names        map[string]int
	resolution   knowledge.Resolution
	mentions     []int
	docs         map[knowledge.SnapshotKey]bool
}

func (ix *Index) view(ctx context.Context, allowed knowledge.AllowedSet, fp knowledge.Fingerprint) (*view, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	if ix.down != nil {
		return nil, ix.down
	}
	v := &view{published: map[knowledge.SnapshotKey]bool{}, entities: map[string]*entity{}, docEntities: map[knowledge.SnapshotKey]map[string]bool{}}
	keys := map[string]string{}
	for snap, g := range ix.snaps {
		if snap.Fingerprint != fp || !allowed.Has(snap.Key) {
			continue
		}
		v.docs = append(v.docs, snap.Key)
		v.published[snap.Key] = true
		v.mentions = append(v.mentions, g.Mentions...)
		v.claims = append(v.claims, g.Claims...)
		for _, e := range g.Entities {
			keys[e.ID] = e.Key
		}
	}
	sort.Slice(v.docs, func(i, j int) bool { return keyLess(v.docs[i], v.docs[j]) })
	sort.SliceStable(v.mentions, func(i, j int) bool { return evidenceLess(v.mentions[i].Evidence, v.mentions[j].Evidence) })
	sort.SliceStable(v.claims, func(i, j int) bool {
		if v.claims[i].Evidence != v.claims[j].Evidence {
			return evidenceLess(v.claims[i].Evidence, v.claims[j].Evidence)
		}
		return v.claims[i].ID < v.claims[j].ID
	})
	for i, m := range v.mentions {
		e := v.entities[m.EntityID]
		if e == nil {
			e = &entity{id: m.EntityID, typ: m.Type, key: keys[m.EntityID], names: map[string]int{}, resolution: m.Resolution, docs: map[knowledge.SnapshotKey]bool{}}
			v.entities[m.EntityID] = e
		}
		e.names[strings.Join(strings.Fields(m.Text), " ")]++
		e.mentions = append(e.mentions, i)
		e.docs[m.Evidence.Key()] = true
		if v.docEntities[m.Evidence.Key()] == nil {
			v.docEntities[m.Evidence.Key()] = map[string]bool{}
		}
		v.docEntities[m.Evidence.Key()][m.EntityID] = true
	}
	return v, nil
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
		Mentions:  len(e.mentions),
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
		if len(a.mentions) != len(b.mentions) {
			return len(a.mentions) > len(b.mentions)
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

func sameArticle(a, b knowledge.SnapshotKey) bool {
	return a.SourceID == b.SourceID && a.Path == b.Path
}
