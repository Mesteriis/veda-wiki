package memindex

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Mesteriis/veda-wiki/internal/knowledge"
)

// Context implements knowledge.Index. Selection, in order:
//
//  1. entities whose allowed name or alias occurs in the query text
//     (entity_match, depth 0);
//  2. the allowed seed snapshots (lexical_hit, depth 0) and the non-generic
//     entities they mention (mentioned_in_hit, depth 1);
//  3. claims touching a selected entity, hop by hop up to Depth
//     (relation_of_selected_entity, depth of the hop).
//
// Items are added until MaxItems or LimitBytes of JSON would be exceeded.
// Nothing is generated: every item carries its evidence.
func (ix *Index) Context(ctx context.Context, allowed knowledge.AllowedSet, fp knowledge.Fingerprint, q knowledge.ContextQuery) (knowledge.ContextResult, error) {
	v, err := ix.view(ctx, allowed, fp)
	if err != nil {
		return knowledge.ContextResult{}, err
	}
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
	for _, e := range v.sortedEntities(nil) {
		for name := range e.names {
			n := strings.ToLower(name)
			if query != "" && len([]rune(n)) >= 2 && containsWord(query, n) {
				selected[e.id] = 0
				b.entity(v, e, knowledge.ReasonEntityMatch, 0)
				break
			}
		}
	}
	for _, seed := range q.Seeds {
		if !v.published[seed] {
			continue
		}
		key := seed
		b.add("doc:"+seed.SourceID+"/"+seed.Path+"@"+seed.SHA, knowledge.ContextItem{Kind: knowledge.ItemDocument, Reason: knowledge.ReasonLexicalHit, Key: &key, Evidence: []knowledge.Evidence{}})
		for _, id := range sortedIDs(v.docEntities[seed]) {
			e := v.entities[id]
			if _, ok := selected[id]; ok || v.generic(e) {
				continue
			}
			selected[id] = 1
			b.entity(v, e, knowledge.ReasonMentionedInHit, 1)
		}
	}
	frontier := map[string]bool{}
	for id := range selected {
		frontier[id] = true
	}
	for hop := 1; hop <= depth && len(frontier) > 0; hop++ {
		next := map[string]bool{}
		for _, c := range v.claims {
			if !frontier[c.HeadID] && !frontier[c.TailID] {
				continue
			}
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

func (b *contextBuilder) entity(v *view, e *entity, reason string, depth int) {
	s := v.summary(e)
	b.add(e.id, knowledge.ContextItem{Kind: knowledge.ItemEntity, Reason: reason, Depth: depth, Entity: &s.Entity, Evidence: v.mentionEvidence(e.id, v.docsOf(e)...)})
}

func (b *contextBuilder) add(id string, item knowledge.ContextItem) {
	if b.seen[id] {
		return
	}
	data, err := json.Marshal(item)
	// Bytes is the exact size of the JSON array: "[]", the items and a
	// comma before every item but the first.
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

// docsOf lists the allowed documents of e in key order, at most
// MaxEvidencePerReason.
func (v *view) docsOf(e *entity) []knowledge.SnapshotKey {
	var out []knowledge.SnapshotKey
	for _, k := range v.docs {
		if e.docs[k] && len(out) < knowledge.MaxEvidencePerReason {
			out = append(out, k)
		}
	}
	return out
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
