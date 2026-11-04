package neo4jindex

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"

	"github.com/Mesteriis/veda-wiki/internal/knowledge"
)

// Context implements knowledge.Index with memindex's selection, in order:
//
//  1. entities whose allowed name or alias occurs in the query text
//     (entity_match, depth 0);
//  2. the allowed seed snapshots (lexical_hit, depth 0) and the non-generic
//     entities they mention (mentioned_in_hit, depth 1);
//  3. claims of allowed documents touching a selected entity, hop by hop up
//     to Depth (relation_of_selected_entity, depth of the hop).
//
// Items are added until MaxItems or LimitBytes of JSON would be exceeded.
func (ix *Index) Context(ctx context.Context, allowed knowledge.AllowedSet, fp knowledge.Fingerprint, q knowledge.ContextQuery) (knowledge.ContextResult, error) {
	var out knowledge.ContextResult
	err := ix.withView(ctx, allowed, fp, func(tx neo4j.ManagedTransaction, v *view) error {
		res, err := v.context(ctx, tx, q)
		out = res
		return err
	})
	if err != nil {
		return knowledge.ContextResult{}, err
	}
	return out, nil
}

// contextStep is one item of steps 1 and 2: an entity or a seed document.
type contextStep struct {
	entity *entity
	seed   *knowledge.SnapshotKey
	reason string
	depth  int
}

func (v *view) context(ctx context.Context, tx neo4j.ManagedTransaction, q knowledge.ContextQuery) (knowledge.ContextResult, error) {
	limits := knowledge.SchemaV1().Limits
	depth := q.Depth
	if depth <= 0 {
		depth = limits.DepthDefault
	}
	depth = min(depth, limits.DepthMax)
	limitBytes := q.LimitBytes
	if limitBytes <= 0 {
		limitBytes = knowledge.DefaultContextBytes
	}
	limitBytes = min(limitBytes, knowledge.MaxContextBytes)
	b := &contextBuilder{out: knowledge.ContextResult{Items: []knowledge.ContextItem{}, Bytes: 2}, limit: limitBytes, maxItems: knowledge.ClampLimit(q.MaxItems), seen: map[string]bool{}}
	query := strings.ToLower(strings.Join(strings.Fields(q.Text), " "))
	selected := map[string]int{}
	var steps []contextStep
	for _, e := range v.sortedEntities(nil) {
		for name := range e.names {
			n := strings.ToLower(name)
			if query != "" && len([]rune(n)) >= 2 && containsWord(query, n) {
				selected[e.id] = 0
				steps = append(steps, contextStep{entity: e, reason: knowledge.ReasonEntityMatch})
				break
			}
		}
	}
	for _, seed := range q.Seeds {
		if !v.published[seed] {
			continue
		}
		key := seed
		steps = append(steps, contextStep{seed: &key, reason: knowledge.ReasonLexicalHit})
		for _, id := range sortedIDs(v.docEntities[seed]) {
			e := v.entities[id]
			if _, ok := selected[id]; ok || v.generic(e) {
				continue
			}
			selected[id] = 1
			steps = append(steps, contextStep{entity: e, reason: knowledge.ReasonMentionedInHit, depth: 1})
		}
	}
	docs, entities := map[knowledge.SnapshotKey]bool{}, map[string]bool{}
	for _, s := range steps {
		if s.entity != nil {
			entities[s.entity.id] = true
			for _, k := range v.docsOf(s.entity, knowledge.MaxEvidencePerReason) {
				docs[k] = true
			}
		}
	}
	quotes, err := v.quotes(ctx, tx, docs, entities)
	if err != nil {
		return b.out, err
	}
	for _, s := range steps {
		if s.seed != nil {
			b.add("doc:"+s.seed.SourceID+"/"+s.seed.Path+"@"+s.seed.SHA, knowledge.ContextItem{Kind: knowledge.ItemDocument, Reason: s.reason, Key: s.seed, Evidence: []knowledge.Evidence{}})
			continue
		}
		ev := []knowledge.Evidence{}
		for _, k := range v.docsOf(s.entity, knowledge.MaxEvidencePerReason) {
			ev = append(ev, quotes[k][s.entity.id]...)
		}
		summary := v.summary(s.entity)
		b.add(s.entity.id, knowledge.ContextItem{Kind: knowledge.ItemEntity, Reason: s.reason, Depth: s.depth, Entity: &summary.Entity, Evidence: ev})
	}
	frontier := map[string]bool{}
	for id := range selected {
		frontier[id] = true
	}
	for hop := 1; hop <= depth && len(frontier) > 0; hop++ {
		claims, err := v.claims(ctx, tx, v.docs, sortedIDs(frontier), unlimited)
		if err != nil {
			return b.out, err
		}
		next := map[string]bool{}
		for _, c := range claims {
			claim := c
			b.add(c.ID, knowledge.ContextItem{Kind: knowledge.ItemClaim, Reason: knowledge.ReasonRelatedToEntity, Depth: hop, Claim: &claim, Evidence: []knowledge.Evidence{c.Evidence}})
			for _, id := range []string{c.HeadID, c.TailID} {
				if _, ok := selected[id]; !ok {
					selected[id] = hop
					next[id] = true
				}
			}
		}
		frontier = next
	}
	return b.out, nil
}

type contextBuilder struct {
	out      knowledge.ContextResult
	limit    int
	maxItems int
	seen     map[string]bool
}

func (b *contextBuilder) add(id string, item knowledge.ContextItem) {
	if b.seen[id] {
		return
	}
	data, err := json.Marshal(item)
	// Bytes is the exact size of the JSON array: "[]", the items and a
	// comma before every item but the first (as memindex).
	size := len(data)
	if len(b.out.Items) > 0 {
		size++
	}
	if err != nil || len(b.out.Items) == b.maxItems || b.out.Bytes+size > b.limit {
		b.out.Truncated = true
		return
	}
	b.seen[id] = true
	b.out.Items = append(b.out.Items, item)
	b.out.Bytes += size
}

// containsWord reports whether name occurs in text at word boundaries.
func containsWord(text, name string) bool {
	for from := 0; ; {
		i := strings.Index(text[from:], name)
		if i < 0 {
			return false
		}
		start, end := from+i, from+i+len(name)
		if boundary(text, start-1) && boundary(text, end) {
			return true
		}
		from = start + 1
	}
}

func boundary(text string, i int) bool {
	if i < 0 || i >= len(text) {
		return true
	}
	c := text[i]
	return c < 0x80 && !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.')
}
