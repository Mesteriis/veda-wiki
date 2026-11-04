package neo4jindex

import (
	"context"
	"math"
	"sort"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"

	"github.com/Mesteriis/veda-wiki/internal/knowledge"
)

// unlimited stands for "no row limit" in fetch parameters.
const unlimited = math.MaxInt32

// fetchMentions returns mentions of the given allowed documents ($docs are
// snapshot IDs the view already admitted, sorted in Go key order),
// optionally only of $entities, at most $per per document and entity, at
// most $limit, in Go's evidence order: list position, then evidence rank.
const fetchMentions = `
UNWIND range(0, size($docs) - 1) AS di
WITH di, $docs[di] AS sid
MATCH (s:Snapshot {id: sid})
WHERE s.complete = true
MATCH (s)-[:HAS]->(ev:Evidence)<-[:AT]-(m:Mention)-[:OF]->(e:Entity)
WHERE $entities IS NULL OR e.id IN $entities
WITH di, s, e, ev, m
ORDER BY di, ev.rank
WITH di, s, e, collect([ev, m])[..$per] AS pairs
UNWIND pairs AS p
WITH di, s, e, p[0] AS ev, p[1] AS m
RETURN s.source_id, s.path, s.sha, s.fp, ev.block_id, ev.quote, ev.start, ev.end, ev.src_start, ev.src_end, ev.exact,
       e.id, m.type, m.text, m.score, m.res_method, m.res_basis, m.seq
ORDER BY di, ev.rank
LIMIT $limit`

// fetchClaims returns claims of the given allowed documents, optionally
// only those with an end in $entities, at most $limit, in Go's evidence
// order (list position, then evidence rank).
const fetchClaims = `
UNWIND range(0, size($docs) - 1) AS di
WITH di, $docs[di] AS sid
MATCH (s:Snapshot {id: sid})
WHERE s.complete = true
MATCH (s)-[:HAS]->(ev:Evidence)<-[:AT]-(c:Claim)
MATCH (c)-[:HEAD]->(h:Entity)
MATCH (c)-[:TAIL]->(t:Entity)
WHERE $entities IS NULL OR h.id IN $entities OR t.id IN $entities
RETURN s.source_id, s.path, s.sha, s.fp, ev.block_id, ev.quote, ev.start, ev.end, ev.src_start, ev.src_end, ev.exact,
       c.claim_id, c.type, h.id, t.id, c.score, c.head_score, c.tail_score, c.status, c.markers, c.seq
ORDER BY di, ev.rank
LIMIT $limit`

type mentionRow struct {
	knowledge.Mention
	seq int64
}

type claimRow struct {
	knowledge.Claim
	seq int64
}

// mentionsQuery selects mentions of docs (keys the view admitted).
type mentionsQuery struct {
	docs     []knowledge.SnapshotKey
	entities []string // nil: every entity
	per      int      // per document and entity; 0: unlimited
	limit    int      // 0: unlimited
}

func (q mentionsQuery) params(v *view) map[string]any {
	p := map[string]any{"docs": v.docIDs(q.docs), "entities": nil, "per": positive(q.per), "limit": positive(q.limit)}
	if q.entities != nil {
		p["entities"] = q.entities
	}
	return p
}

func positive(n int) int {
	if n <= 0 {
		return unlimited
	}
	return n
}

func (v *view) mentions(ctx context.Context, tx neo4j.ManagedTransaction, q mentionsQuery) ([]knowledge.Mention, error) {
	if len(q.docs) == 0 || q.entities != nil && len(q.entities) == 0 {
		return nil, nil
	}
	recs, err := collect(ctx, tx, fetchMentions, q.params(v))
	if err != nil {
		return nil, err
	}
	out := make([]mentionRow, 0, len(recs))
	for _, r := range recs {
		x := r.Values
		out = append(out, mentionRow{Mention: knowledge.Mention{
			EntityID:   str(x[11]),
			Type:       str(x[12]),
			Text:       str(x[13]),
			Score:      float(x[14]),
			Resolution: knowledge.Resolution{Method: knowledge.ResolutionMethod(str(x[15])), Basis: str(x[16])},
			Evidence:   evidenceOf(x),
		}, seq: integer(x[17])})
	}
	// Evidence order, then the order of the published graph (memindex keeps
	// it with a stable sort).
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].Evidence, out[j].Evidence
		if evidenceLess(a, b) || evidenceLess(b, a) {
			return evidenceLess(a, b)
		}
		return out[i].seq < out[j].seq
	})
	mentions := make([]knowledge.Mention, len(out))
	for i, m := range out {
		mentions[i] = m.Mention
	}
	return mentions, nil
}

// claims selects claims of docs, optionally touching entities.
func (v *view) claims(ctx context.Context, tx neo4j.ManagedTransaction, docs []knowledge.SnapshotKey, entities []string, limit int) ([]knowledge.Claim, error) {
	if len(docs) == 0 || entities != nil && len(entities) == 0 {
		return nil, nil
	}
	p := map[string]any{"docs": v.docIDs(docs), "entities": nil, "limit": positive(limit)}
	if entities != nil {
		p["entities"] = entities
	}
	recs, err := collect(ctx, tx, fetchClaims, p)
	if err != nil {
		return nil, err
	}
	out := make([]claimRow, 0, len(recs))
	for _, r := range recs {
		x := r.Values
		c := knowledge.Claim{ID: str(x[11]), Relation: str(x[12]), HeadID: str(x[13]), TailID: str(x[14]),
			HeadScore: float(x[16]), TailScore: float(x[17]), Status: knowledge.ClaimStatus(str(x[18])), Evidence: evidenceOf(x)}
		if f, ok := x[15].(float64); ok {
			c.Score = &f
		}
		if markers, _ := x[19].([]any); len(markers) > 0 {
			for _, m := range markers {
				c.Markers = append(c.Markers, str(m))
			}
		}
		out = append(out, claimRow{Claim: c, seq: integer(x[20])})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Evidence != out[j].Evidence {
			return evidenceLess(out[i].Evidence, out[j].Evidence)
		}
		return out[i].ID < out[j].ID
	})
	claims := make([]knowledge.Claim, len(out))
	for i, c := range out {
		claims[i] = c.Claim
	}
	return claims, nil
}

func evidenceOf(x []any) knowledge.Evidence {
	return knowledge.Evidence{SourceID: str(x[0]), Path: str(x[1]), SHA: str(x[2]), Fingerprint: knowledge.Fingerprint(str(x[3])),
		BlockID: str(x[4]), Quote: str(x[5]), Start: int(integer(x[6])), End: int(integer(x[7])),
		SourceStart: int(integer(x[8])), SourceEnd: int(integer(x[9])), Exact: x[10] == true}
}

func float(v any) float64 {
	f, _ := v.(float64)
	return f
}

func integer(v any) int64 {
	n, _ := v.(int64)
	return n
}
