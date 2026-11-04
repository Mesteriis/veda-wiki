package neo4jindex

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"

	"github.com/Mesteriis/veda-wiki/internal/knowledge"
)

// clearSnapshot removes the content of a snapshot and marks it incomplete;
// it returns the entities the removed mentions and claims referred to.
const clearSnapshot = `
MATCH (s:Snapshot {id: $sid})
SET s.complete = false
WITH s
OPTIONAL MATCH (s)-[:HAS]->(ev:Evidence)
OPTIONAL MATCH (ev)<-[:AT]-(x)
OPTIONAL MATCH (x)-[:OF|HEAD|TAIL]->(e:Entity)
WITH s, collect(DISTINCT e.id) AS touched, collect(DISTINCT x) AS xs, collect(DISTINCT ev) AS evs
FOREACH (n IN xs | DETACH DELETE n)
FOREACH (n IN evs | DETACH DELETE n)
RETURN touched`

const mergeSnapshot = `
MERGE (s:Snapshot {id: $sid})
SET s += $props, s.complete = false`

const mergeEntities = `
UNWIND $rows AS r
MERGE (e:Entity {id: r.id})
ON CREATE SET e.type = r.type, e.key = r.key`

const mergeMentions = `
MATCH (s:Snapshot {id: $sid})
UNWIND $rows AS r
MATCH (e:Entity {id: r.entity})
MERGE (ev:Evidence {id: r.ev_id})
SET ev += r.ev
MERGE (m:Mention {id: r.id})
SET m += r.props
MERGE (s)-[:HAS]->(ev)
MERGE (m)-[:AT]->(ev)
MERGE (m)-[:OF]->(e)`

const mergeClaims = `
MATCH (s:Snapshot {id: $sid})
UNWIND $rows AS r
MATCH (h:Entity {id: r.head}), (t:Entity {id: r.tail})
MERGE (ev:Evidence {id: r.ev_id})
SET ev += r.ev
MERGE (c:Claim {id: r.id})
SET c += r.props
MERGE (s)-[:HAS]->(ev)
MERGE (c)-[:AT]->(ev)
MERGE (c)-[:HEAD]->(h)
MERGE (c)-[:TAIL]->(t)`

const completeSnapshot = `MATCH (s:Snapshot {id: $sid}) SET s.complete = true`

// deleteOrphans removes entities that no mention or claim refers to.
const deleteOrphans = `
UNWIND $ids AS id
MATCH (e:Entity {id: id})
WHERE NOT (e)<-[:OF|HEAD|TAIL]-()
DETACH DELETE e`

// retireSnapshots removes snapshots with their evidence, mentions and claims.
const retireSnapshots = `
UNWIND $sids AS sid
MATCH (s:Snapshot {id: sid})
OPTIONAL MATCH (s)-[:HAS]->(ev:Evidence)
OPTIONAL MATCH (ev)<-[:AT]-(x)
OPTIONAL MATCH (x)-[:OF|HEAD|TAIL]->(e:Entity)
WITH collect(DISTINCT s) AS ss, collect(DISTINCT e.id) AS touched, collect(DISTINCT x) AS xs, collect(DISTINCT ev) AS evs
FOREACH (n IN xs | DETACH DELETE n)
FOREACH (n IN evs | DETACH DELETE n)
FOREACH (n IN ss | DETACH DELETE n)
RETURN touched`

// Publish implements knowledge.Index: one write transaction clears the
// snapshot, merges its graph and sets complete=true last, so readers see
// the previous graph or the new one, never a part. Entities left without
// any mention or claim by the replacement are removed.
func (ix *Index) Publish(ctx context.Context, snap knowledge.Snapshot, g knowledge.Graph) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ix.validate(snap, g); err != nil {
		return err
	}
	sid := SnapshotID(snap.Fingerprint, snap.Key)
	entities, mentions, claims := rows(sid, g)
	return ix.write(ctx, ix.publish, func(tx neo4j.ManagedTransaction) error {
		recs, err := collect(ctx, tx, clearSnapshot, map[string]any{"sid": sid})
		if err != nil {
			return err
		}
		var touched []any
		if len(recs) == 1 {
			touched, _ = recs[0].Values[0].([]any)
		}
		k := snap.Key
		props := map[string]any{"key": k.Article(), "source_id": k.SourceID, "path": k.Path, "sha": k.SHA,
			"fp": string(snap.Fingerprint), "generation": string(snap.Fingerprint), "doc_node": knowledge.DocumentNodeID(k)}
		if _, err = collect(ctx, tx, mergeSnapshot, map[string]any{"sid": sid, "props": props}); err != nil {
			return err
		}
		for _, step := range []struct {
			cypher string
			rows   []any
		}{{mergeEntities, entities}, {mergeMentions, mentions}, {mergeClaims, claims}} {
			for start := 0; start < len(step.rows); start += batchSize {
				batch := step.rows[start:min(start+batchSize, len(step.rows))]
				if _, err = collect(ctx, tx, step.cypher, map[string]any{"sid": sid, "rows": batch}); err != nil {
					return err
				}
			}
		}
		if len(touched) > 0 {
			if _, err = collect(ctx, tx, deleteOrphans, map[string]any{"ids": touched}); err != nil {
				return err
			}
		}
		_, err = collect(ctx, tx, completeSnapshot, map[string]any{"sid": sid})
		return err
	})
}

// validate checks the graph against the schema and the snapshot before
// anything is written: declared entity and relation types, relation ends
// allowed by the schema, mentions and claims that refer to entities of the
// graph, and evidence of this snapshot.
func (ix *Index) validate(snap knowledge.Snapshot, g knowledge.Graph) error {
	if snap.Key.SourceID == "" || snap.Key.Path == "" || snap.Key.SHA == "" || snap.Fingerprint == "" {
		return invalidGraph{"incomplete snapshot key"}
	}
	types := map[string]string{}
	for _, e := range g.Entities {
		if e.ID == "" || !ix.schema.HasEntity(e.Type) {
			return invalidGraph{"entity with an empty ID or an undeclared type"}
		}
		if t, ok := types[e.ID]; ok && t != e.Type {
			return invalidGraph{"entity listed with two types"}
		}
		types[e.ID] = e.Type
	}
	evidenceOK := func(ev knowledge.Evidence) bool {
		return ev.Key() == snap.Key && ev.Fingerprint == snap.Fingerprint && ev.Start >= 0 && ev.End >= ev.Start
	}
	for _, m := range g.Mentions {
		if types[m.EntityID] != m.Type || m.Type == "" {
			return invalidGraph{"mention of an entity outside the graph"}
		}
		if !evidenceOK(m.Evidence) {
			return invalidGraph{"mention evidence of another snapshot"}
		}
	}
	for _, c := range g.Claims {
		head, tail := types[c.HeadID], types[c.TailID]
		if head == "" || tail == "" || !ix.schema.ValidRelation(c.Relation, head, tail) {
			return invalidGraph{"claim with an undeclared relation or invalid ends"}
		}
		if c.Status != knowledge.ClaimExtracted && c.Status != knowledge.ClaimUncertain {
			return invalidGraph{"claim with an unknown status"}
		}
		if !evidenceOK(c.Evidence) {
			return invalidGraph{"claim evidence of another snapshot"}
		}
	}
	return nil
}

func evidenceProps(ev knowledge.Evidence, rank int) map[string]any {
	return map[string]any{"block_id": ev.BlockID, "quote": ev.Quote, "start": ev.Start, "end": ev.End,
		"src_start": ev.SourceStart, "src_end": ev.SourceEnd, "exact": ev.Exact, "rank": rank}
}

// ranks is the position of each item in Go's read order within one
// snapshot: evidence block, start and end compared as Go strings and
// integers, then tie (the graph position for mentions, the claim ID for
// claims). Reads order by snapshot position in the Go-sorted $docs list and
// by this rank, never by strings in Cypher, whose collation is not Go's.
func ranks(n int, ev func(int) knowledge.Evidence, tie func(i, j int) bool) []int {
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		x, y := ev(order[a]), ev(order[b])
		if x.BlockID != y.BlockID {
			return x.BlockID < y.BlockID
		}
		if x.Start != y.Start {
			return x.Start < y.Start
		}
		if x.End != y.End {
			return x.End < y.End
		}
		return tie(order[a], order[b])
	})
	out := make([]int, n)
	for rank, i := range order {
		out[i] = rank
	}
	return out
}

// rows converts the graph into publish parameters. Node IDs derive from the
// snapshot ID and the position in the graph. Entities that no mention or
// claim refers to are left out, so they never become orphans.
func rows(sid string, g knowledge.Graph) (entities, mentions, claims []any) {
	used := map[string]bool{}
	for _, m := range g.Mentions {
		used[m.EntityID] = true
	}
	for _, c := range g.Claims {
		used[c.HeadID], used[c.TailID] = true, true
	}
	seen := map[string]bool{}
	for _, e := range g.Entities {
		if used[e.ID] && !seen[e.ID] {
			seen[e.ID] = true
			entities = append(entities, map[string]any{"id": e.ID, "type": e.Type, "key": e.Key})
		}
	}
	mentionRank := ranks(len(g.Mentions), func(i int) knowledge.Evidence { return g.Mentions[i].Evidence }, func(i, j int) bool { return i < j })
	for i, m := range g.Mentions {
		pos := strconv.Itoa(i)
		mentions = append(mentions, map[string]any{
			"id": sid + ":m:" + pos, "ev_id": sid + ":me:" + pos, "entity": m.EntityID, "ev": evidenceProps(m.Evidence, mentionRank[i]),
			"props": map[string]any{"seq": i, "type": m.Type, "text": m.Text, "surface": surface(m.Text), "score": m.Score,
				"res_method": string(m.Resolution.Method), "res_basis": m.Resolution.Basis},
		})
	}
	claimRank := ranks(len(g.Claims), func(i int) knowledge.Evidence { return g.Claims[i].Evidence }, func(i, j int) bool {
		if g.Claims[i].ID != g.Claims[j].ID {
			return g.Claims[i].ID < g.Claims[j].ID
		}
		return i < j
	})
	for i, c := range g.Claims {
		pos := strconv.Itoa(i)
		props := map[string]any{"seq": i, "claim_id": c.ID, "type": c.Relation, "status": string(c.Status),
			"markers": append([]string{}, c.Markers...), "head_score": c.HeadScore, "tail_score": c.TailScore}
		if c.Score != nil {
			props["score"] = *c.Score
		}
		claims = append(claims, map[string]any{"id": sid + ":c:" + pos, "ev_id": sid + ":ce:" + pos,
			"head": c.HeadID, "tail": c.TailID, "ev": evidenceProps(c.Evidence, claimRank[i]), "props": props})
	}
	return entities, mentions, claims
}

// surface is a mention text with white space runs collapsed: the form names
// and aliases are counted by.
func surface(text string) string { return strings.Join(strings.Fields(text), " ") }

// Retire implements knowledge.Index: it deletes only the snapshots'
// evidence, mentions and claims, then entities nothing refers to any more.
func (ix *Index) Retire(ctx context.Context, snaps []knowledge.Snapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(snaps) == 0 {
		return nil
	}
	sids := make([]string, len(snaps))
	for i, s := range snaps {
		sids[i] = SnapshotID(s.Fingerprint, s.Key)
	}
	return ix.write(ctx, ix.publish, func(tx neo4j.ManagedTransaction) error {
		recs, err := collect(ctx, tx, retireSnapshots, map[string]any{"sids": sids})
		if err != nil || len(recs) == 0 {
			return err
		}
		touched, _ := recs[0].Values[0].([]any)
		if len(touched) == 0 {
			return nil
		}
		_, err = collect(ctx, tx, deleteOrphans, map[string]any{"ids": touched})
		return err
	})
}

// Snapshots implements knowledge.Index: complete snapshots only.
func (ix *Index) Snapshots(ctx context.Context) ([]knowledge.Snapshot, error) {
	if err := ix.ensureSchema(ctx); err != nil {
		return nil, err
	}
	var out []knowledge.Snapshot
	err := ix.read(ctx, func(tx neo4j.ManagedTransaction) error {
		out = nil
		recs, err := collect(ctx, tx, `MATCH (s:Snapshot) WHERE s.complete = true RETURN s.source_id, s.path, s.sha, s.fp`, nil)
		if err != nil {
			return err
		}
		for _, r := range recs {
			out = append(out, knowledge.Snapshot{Key: knowledge.SnapshotKey{SourceID: str(r.Values[0]), Path: str(r.Values[1]), SHA: str(r.Values[2])}, Fingerprint: knowledge.Fingerprint(str(r.Values[3]))})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Key != out[j].Key {
			return keyLess(out[i].Key, out[j].Key)
		}
		return out[i].Fingerprint < out[j].Fingerprint
	})
	if out == nil {
		out = []knowledge.Snapshot{}
	}
	return out, nil
}
