package memindex

import (
	"context"
	"sort"
	"strings"

	"github.com/Mesteriis/veda-wiki/internal/knowledge"
)

// Entities implements knowledge.Index.
func (ix *Index) Entities(ctx context.Context, allowed knowledge.AllowedSet, fp knowledge.Fingerprint, q knowledge.EntityQuery) (knowledge.EntityPage, error) {
	v, err := ix.view(ctx, allowed, fp)
	if err != nil {
		return knowledge.EntityPage{}, err
	}
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
	page := knowledge.EntityPage{Entities: []knowledge.EntitySummary{}, Total: len(matches)}
	for i := offset; i < len(matches) && i < offset+limit; i++ {
		page.Entities = append(page.Entities, v.summary(matches[i]))
	}
	if offset+limit < len(matches) {
		page.Next = offset + limit
	}
	return page, nil
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
	v, err := ix.view(ctx, allowed, fp)
	if err != nil {
		return knowledge.EntityCard{}, err
	}
	e := v.entities[id]
	if e == nil {
		return knowledge.EntityCard{}, knowledge.ErrNotFound
	}
	limit = knowledge.ClampLimit(limit)
	card := knowledge.EntityCard{EntitySummary: v.summary(e), MentionList: []knowledge.Mention{}, Claims: []knowledge.Claim{}, DocumentList: []knowledge.SnapshotKey{}, Candidates: v.candidates(e)}
	for _, i := range e.mentions {
		if len(card.MentionList) == limit {
			card.Truncated = true
			break
		}
		card.MentionList = append(card.MentionList, v.mentions[i])
	}
	for _, c := range v.claims {
		if c.HeadID != id && c.TailID != id {
			continue
		}
		if len(card.Claims) == limit {
			card.Truncated = true
			break
		}
		card.Claims = append(card.Claims, c)
	}
	for _, k := range v.docs {
		if !e.docs[k] {
			continue
		}
		if len(card.DocumentList) == limit {
			card.Truncated = true
			break
		}
		card.DocumentList = append(card.DocumentList, k)
	}
	if len(card.Candidates) > limit {
		card.Candidates, card.Truncated = card.Candidates[:limit], true
	}
	return card, nil
}

// Article implements knowledge.Index.
func (ix *Index) Article(ctx context.Context, allowed knowledge.AllowedSet, fp knowledge.Fingerprint, key knowledge.SnapshotKey, limit int) (knowledge.ArticleView, error) {
	v, err := ix.view(ctx, allowed, fp)
	if err != nil {
		return knowledge.ArticleView{}, err
	}
	if !v.published[key] {
		return knowledge.ArticleView{}, knowledge.ErrNotFound
	}
	limit = knowledge.ClampLimit(limit)
	out := knowledge.ArticleView{Key: key, Entities: []knowledge.EntitySummary{}, Mentions: []knowledge.Mention{}, Claims: []knowledge.Claim{}}
	for _, e := range v.sortedEntities(func(e *entity) bool { return e.docs[key] }) {
		if len(out.Entities) == limit {
			out.Truncated = true
			break
		}
		out.Entities = append(out.Entities, v.summary(e))
	}
	for _, m := range v.mentions {
		if m.Evidence.Key() != key {
			continue
		}
		if len(out.Mentions) == limit {
			out.Truncated = true
			break
		}
		out.Mentions = append(out.Mentions, m)
	}
	for _, c := range v.claims {
		if c.Evidence.Key() != key {
			continue
		}
		if len(out.Claims) == limit {
			out.Truncated = true
			break
		}
		out.Claims = append(out.Claims, c)
	}
	related := v.related(key)
	if len(related) > limit {
		related, out.Truncated = related[:limit], true
	}
	out.Related = related
	return out, nil
}

// related lists other allowed snapshots (of other articles) related to key,
// with every reason: a shared non-generic entity, an extracted claim of one
// document whose end entity the other mentions, and a candidate entity
// match. Ranked by reason count, then key.
func (v *view) related(key knowledge.SnapshotKey) []knowledge.RelatedArticle {
	reasons := map[knowledge.SnapshotKey][]knowledge.Reason{}
	mine := v.docEntities[key]
	useful := func(id string) bool { e := v.entities[id]; return e != nil && !v.generic(e) }
	for _, other := range v.docs {
		if sameArticle(other, key) {
			continue
		}
		theirs := v.docEntities[other]
		for _, id := range sortedIDs(mine) {
			if theirs[id] && useful(id) {
				reasons[other] = append(reasons[other], knowledge.Reason{Kind: knowledge.ReasonSharedEntity, EntityID: id, Evidence: v.mentionEvidence(id, key, other)})
			}
		}
		for _, c := range v.claims {
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
		for _, id := range sortedIDs(mine) {
			for _, cand := range v.candidates(v.entities[id]) {
				if theirs[cand.EntityID] && !mine[cand.EntityID] {
					reasons[other] = append(reasons[other], knowledge.Reason{Kind: knowledge.ReasonCandidate, EntityID: cand.EntityID, Evidence: v.mentionEvidence(cand.EntityID, other)})
				}
			}
		}
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
	return out
}

// mentionEvidence is up to MaxEvidencePerReason mentions of id in docs.
func (v *view) mentionEvidence(id string, docs ...knowledge.SnapshotKey) []knowledge.Evidence {
	out := []knowledge.Evidence{}
	for _, d := range docs {
		n := 0
		for _, i := range v.entities[id].mentions {
			if m := v.mentions[i]; m.Evidence.Key() == d && n < knowledge.MaxEvidencePerReason {
				out = append(out, m.Evidence)
				n++
			}
		}
	}
	return out
}

func sortedIDs(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
