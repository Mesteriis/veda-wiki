package neo4jindex

import (
	"context"
	"sort"
	"strings"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"

	"github.com/Mesteriis/veda-wiki/internal/knowledge"
)

// Entities implements knowledge.Index.
func (ix *Index) Entities(ctx context.Context, allowed knowledge.AllowedSet, fp knowledge.Fingerprint, q knowledge.EntityQuery) (knowledge.EntityPage, error) {
	var page knowledge.EntityPage
	err := ix.withView(ctx, allowed, fp, func(_ neo4j.ManagedTransaction, v *view) error {
		text := strings.ToLower(strings.Join(strings.Fields(q.Text), " "))
		matches := v.sortedEntities(func(e *entity) bool {
			if q.Type != "" && e.typ != q.Type {
				return false
			}
			if q.SourceID != "" && !inSource(e, q.SourceID) {
				return false
			}
			if text == "" {
				return true
			}
			for name := range e.names {
				if strings.Contains(strings.ToLower(name), text) {
					return true
				}
			}
			return false
		})
		limit, offset := knowledge.ClampLimit(q.Limit), max(q.Offset, 0)
		page = knowledge.EntityPage{Entities: []knowledge.EntitySummary{}, Total: len(matches)}
		for i := offset; i < len(matches) && i < offset+limit; i++ {
			page.Entities = append(page.Entities, v.summary(matches[i]))
		}
		if offset+limit < len(matches) {
			page.Next = offset + limit
		}
		return nil
	})
	return page, err
}

func inSource(e *entity, sourceID string) bool {
	for k := range e.docs {
		if k.SourceID == sourceID {
			return true
		}
	}
	return false
}

// Entity implements knowledge.Index.
func (ix *Index) Entity(ctx context.Context, allowed knowledge.AllowedSet, fp knowledge.Fingerprint, id string, limit int) (knowledge.EntityCard, error) {
	var card knowledge.EntityCard
	err := ix.withView(ctx, allowed, fp, func(tx neo4j.ManagedTransaction, v *view) error {
		e := v.entities[id]
		if e == nil {
			return knowledge.ErrNotFound
		}
		limit = knowledge.ClampLimit(limit)
		card = knowledge.EntityCard{EntitySummary: v.summary(e), MentionList: []knowledge.Mention{}, Claims: []knowledge.Claim{}, DocumentList: []knowledge.SnapshotKey{}, Candidates: v.candidates(e)}
		docs := v.docsOf(e, unlimited)
		mentions, err := v.mentions(ctx, tx, mentionsQuery{docs: docs, entities: []string{id}, limit: limit + 1})
		if err != nil {
			return err
		}
		claims, err := v.claims(ctx, tx, v.docs, []string{id}, limit+1)
		if err != nil {
			return err
		}
		card.MentionList, card.Truncated = cut(mentions, limit, card.MentionList, card.Truncated)
		card.Claims, card.Truncated = cut(claims, limit, card.Claims, card.Truncated)
		card.DocumentList, card.Truncated = cut(docs, limit, card.DocumentList, card.Truncated)
		if len(card.Candidates) > limit {
			card.Candidates, card.Truncated = card.Candidates[:limit], true
		}
		return nil
	})
	if err != nil {
		return knowledge.EntityCard{}, err
	}
	return card, nil
}

// cut appends at most limit items of in to out and reports truncation.
func cut[T any](in []T, limit int, out []T, truncated bool) ([]T, bool) {
	if len(in) > limit {
		return append(out, in[:limit]...), true
	}
	return append(out, in...), truncated
}

// Article implements knowledge.Index.
func (ix *Index) Article(ctx context.Context, allowed knowledge.AllowedSet, fp knowledge.Fingerprint, key knowledge.SnapshotKey, limit int) (knowledge.ArticleView, error) {
	var out knowledge.ArticleView
	err := ix.withView(ctx, allowed, fp, func(tx neo4j.ManagedTransaction, v *view) error {
		if !v.published[key] {
			return knowledge.ErrNotFound
		}
		limit = knowledge.ClampLimit(limit)
		out = knowledge.ArticleView{Key: key, Entities: []knowledge.EntitySummary{}, Mentions: []knowledge.Mention{}, Claims: []knowledge.Claim{}}
		for _, e := range v.sortedEntities(func(e *entity) bool { return e.docs[key] > 0 }) {
			if len(out.Entities) == limit {
				out.Truncated = true
				break
			}
			out.Entities = append(out.Entities, v.summary(e))
		}
		mentions, err := v.mentions(ctx, tx, mentionsQuery{docs: []knowledge.SnapshotKey{key}, limit: limit + 1})
		if err != nil {
			return err
		}
		own, err := v.claims(ctx, tx, []knowledge.SnapshotKey{key}, nil, unlimited)
		if err != nil {
			return err
		}
		out.Mentions, out.Truncated = cut(mentions, limit, out.Mentions, out.Truncated)
		out.Claims, out.Truncated = cut(own, limit, out.Claims, out.Truncated)
		related, err := v.related(ctx, tx, key, own)
		if err != nil {
			return err
		}
		if len(related) > limit {
			related, out.Truncated = related[:limit], true
		}
		out.Related = related
		return nil
	})
	if err != nil {
		return knowledge.ArticleView{}, err
	}
	return out, nil
}

// pendingQuote is a reason whose quotes are filled in after one fetch: up
// to MaxEvidencePerReason mentions of entity in each of docs.
type pendingQuote struct {
	other  knowledge.SnapshotKey
	index  int
	entity string
	docs   []knowledge.SnapshotKey
}

// related lists other allowed snapshots (of other articles) related to key,
// with every reason: a shared non-generic entity, an extracted claim of one
// document whose end entity the other mentions, and a candidate entity
// match. Ranked by reason count, then key. It reads only claims of key and
// claims that touch an entity of key, both from allowed documents.
func (v *view) related(ctx context.Context, tx neo4j.ManagedTransaction, key knowledge.SnapshotKey, own []knowledge.Claim) ([]knowledge.RelatedArticle, error) {
	mine := v.docEntities[key]
	mineIDs := sortedIDs(mine)
	touching, err := v.claims(ctx, tx, v.docs, mineIDs, unlimited)
	if err != nil {
		return nil, err
	}
	claims := unionClaims(own, touching)
	useful := func(id string) bool { e := v.entities[id]; return e != nil && !v.generic(e) }
	candidates := map[string][]knowledge.Candidate{}
	for _, id := range mineIDs {
		candidates[id] = v.candidates(v.entities[id])
	}
	reasons := map[knowledge.SnapshotKey][]knowledge.Reason{}
	var pending []pendingQuote
	evidenceEntities := map[string]bool{}
	evidenceDocs := map[knowledge.SnapshotKey]bool{key: true}
	addPending := func(other knowledge.SnapshotKey, id string, docs ...knowledge.SnapshotKey) {
		pending = append(pending, pendingQuote{other: other, index: len(reasons[other]) - 1, entity: id, docs: docs})
		evidenceEntities[id] = true
		for _, d := range docs {
			evidenceDocs[d] = true
		}
	}
	for _, other := range v.docs {
		if sameArticle(other, key) {
			continue
		}
		theirs := v.docEntities[other]
		for _, id := range mineIDs {
			if theirs[id] && useful(id) {
				reasons[other] = append(reasons[other], knowledge.Reason{Kind: knowledge.ReasonSharedEntity, EntityID: id})
				addPending(other, id, key, other)
			}
		}
		for _, c := range claims {
			from, to := c.Evidence.Key(), knowledge.SnapshotKey{}
			switch from {
			case key:
				to = other
			case other:
				to = key
			default:
				continue
			}
			ends := v.docEntities[to]
			if ends[c.HeadID] && useful(c.HeadID) || ends[c.TailID] && useful(c.TailID) {
				reasons[other] = append(reasons[other], knowledge.Reason{Kind: knowledge.ReasonRelation, ClaimID: c.ID, Relation: c.Relation, Evidence: []knowledge.Evidence{c.Evidence}})
			}
		}
		for _, id := range mineIDs {
			for _, cand := range candidates[id] {
				if theirs[cand.EntityID] && !mine[cand.EntityID] {
					reasons[other] = append(reasons[other], knowledge.Reason{Kind: knowledge.ReasonCandidate, EntityID: cand.EntityID})
					addPending(other, cand.EntityID, other)
				}
			}
		}
	}
	quotes, err := v.quotes(ctx, tx, evidenceDocs, evidenceEntities)
	if err != nil {
		return nil, err
	}
	for _, p := range pending {
		ev := []knowledge.Evidence{}
		for _, d := range p.docs {
			ev = append(ev, quotes[d][p.entity]...)
		}
		reasons[p.other][p.index].Evidence = ev
	}
	out := []knowledge.RelatedArticle{}
	for k, r := range reasons {
		out = append(out, knowledge.RelatedArticle{Key: k, Reasons: r})
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i].Reasons) != len(out[j].Reasons) {
			return len(out[i].Reasons) > len(out[j].Reasons)
		}
		return keyLess(out[i].Key, out[j].Key)
	})
	return out, nil
}

// quotes fetches up to MaxEvidencePerReason mentions per (document, entity)
// of the given allowed documents and entities.
func (v *view) quotes(ctx context.Context, tx neo4j.ManagedTransaction, docs map[knowledge.SnapshotKey]bool, entities map[string]bool) (map[knowledge.SnapshotKey]map[string][]knowledge.Evidence, error) {
	out := map[knowledge.SnapshotKey]map[string][]knowledge.Evidence{}
	if len(entities) == 0 {
		return out, nil
	}
	var keys []knowledge.SnapshotKey
	for k := range docs {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keyLess(keys[i], keys[j]) })
	mentions, err := v.mentions(ctx, tx, mentionsQuery{docs: keys, entities: sortedIDs(entities), per: knowledge.MaxEvidencePerReason})
	if err != nil {
		return nil, err
	}
	for _, m := range mentions {
		k := m.Evidence.Key()
		if out[k] == nil {
			out[k] = map[string][]knowledge.Evidence{}
		}
		out[k][m.EntityID] = append(out[k][m.EntityID], m.Evidence)
	}
	return out, nil
}

// unionClaims is the union of two claim lists in view order.
func unionClaims(a, b []knowledge.Claim) []knowledge.Claim {
	seen := map[string]bool{}
	var out []knowledge.Claim
	for _, list := range [][]knowledge.Claim{a, b} {
		for _, c := range list {
			id := c.Evidence.SHA + "\x00" + c.Evidence.SourceID + "\x00" + c.Evidence.Path + "\x00" + c.ID
			if !seen[id] {
				seen[id] = true
				out = append(out, c)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Evidence != out[j].Evidence {
			return evidenceLess(out[i].Evidence, out[j].Evidence)
		}
		return out[i].ID < out[j].ID
	})
	return out
}
