package knowledge

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Snapshot is one indexed text under one pipeline: the unit of publication,
// retirement and read filtering.
type Snapshot struct {
	Key         SnapshotKey `json:"key"`
	Fingerprint Fingerprint `json:"pipeline_fingerprint"`
}

// GraphEntity is a canonical entity one snapshot refers to. Its display name
// is not stored here: readers derive names from the mentions they may see.
type GraphEntity struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Key  string `json:"key"`
}

// Graph is the derived graph of one snapshot: its mentions, the canonical
// entities they resolve to and its typed claims. Dropped counts model output
// left out by Go-side rules.
type Graph struct {
	Entities []GraphEntity `json:"entities"`
	Mentions []Mention     `json:"mentions"`
	Claims   []Claim       `json:"claims"`
	Dropped  GraphDropped  `json:"dropped"`
}

// GraphDropped counts relations whose end types the schema does not allow,
// relations whose ends had no canonical key, and repeated spans or claims
// (overlapping chunks report the same span twice).
type GraphDropped struct {
	InvalidRelations int `json:"invalid_relations"`
	UnresolvedEnds   int `json:"unresolved_ends"`
	Duplicates       int `json:"duplicates"`
	// Extractor sums what the extractor service itself removed.
	Extractor DroppedCounts `json:"extractor"`
}

// Add sums two drop counts.
func (d GraphDropped) Add(o GraphDropped) GraphDropped {
	d.InvalidRelations += o.InvalidRelations
	d.UnresolvedEnds += o.UnresolvedEnds
	d.Duplicates += o.Duplicates
	d.Extractor.InvalidSpans += o.Extractor.InvalidSpans
	d.Extractor.PartialSpans += o.Extractor.PartialSpans
	d.Extractor.InvalidRelations += o.Extractor.InvalidRelations
	d.Extractor.OverLimit += o.Extractor.OverLimit
	return d
}

// EntityID is the opaque, stable ID of a canonical key. It reveals neither
// the key nor the source it may be scoped to.
func EntityID(canonicalKey string) string {
	sum := sha256.Sum256([]byte(canonicalKey))
	return "ent-" + hex.EncodeToString(sum[:10])
}

type localEntity struct {
	mention Mention
	ok      bool
}

// BuildGraph turns a validated extraction result into the snapshot's graph.
// Each span becomes a mention resolved with CanonicalKey (explicit aliases,
// technical keys, or names scoped to the snapshot's source). Each relation
// whose end types the schema allows becomes a claim whose evidence is the
// sentence (or sentences) holding both ends; the claim is uncertain when
// Markers finds a marker there. The result is re-validated against blocks,
// so a stored result can never be applied to another text.
func BuildGraph(snap Snapshot, s Schema, blocks []Block, res ExtractResult, aliases Aliases) (Graph, error) {
	req := ExtractRequest{SchemaVersion: ExtractSchemaVersion, Schema: WireSchemaOf(s)}
	byID := map[string]Block{}
	for _, b := range blocks {
		if b.Text != "" {
			req.Blocks = append(req.Blocks, WireBlock{ID: b.ID, Text: b.Text})
			byID[b.ID] = b
		}
	}
	if err := ValidateResult(req, ExtractResult{SchemaVersion: res.SchemaVersion, Model: res.Model, Blocks: res.Blocks}); err != nil {
		return Graph{}, err
	}
	g := Graph{Entities: []GraphEntity{}, Mentions: []Mention{}, Claims: []Claim{}}
	entities := map[string]GraphEntity{}
	mentions := map[string]int{}
	claims := map[string]int{}
	for _, br := range res.Blocks {
		block := byID[br.ID]
		if br.Dropped != nil {
			g.Dropped.Extractor.InvalidSpans += br.Dropped.InvalidSpans
			g.Dropped.Extractor.PartialSpans += br.Dropped.PartialSpans
			g.Dropped.Extractor.InvalidRelations += br.Dropped.InvalidRelations
			g.Dropped.Extractor.OverLimit += br.Dropped.OverLimit
		}
		text := []rune(block.Text)
		locals := map[string]localEntity{}
		for _, e := range br.Entities {
			key, resolution := CanonicalKey(Name{SourceID: snap.Key.SourceID, Type: e.Type, Text: e.Text}, aliases)
			if key == "" {
				locals[e.LocalID] = localEntity{}
				continue
			}
			id := EntityID(key)
			entities[id] = GraphEntity{ID: id, Type: e.Type, Key: key}
			m := Mention{EntityID: id, Type: e.Type, Text: e.Text, Score: e.Confidence, Resolution: resolution, Evidence: evidence(snap, block, text, e.Start, e.End)}
			locals[e.LocalID] = localEntity{mention: m, ok: true}
			dedupe := br.ID + "\x00" + e.Type + "\x00" + strconv.Itoa(e.Start) + "\x00" + strconv.Itoa(e.End)
			if i, seen := mentions[dedupe]; seen {
				g.Dropped.Duplicates++
				if m.Score > g.Mentions[i].Score {
					g.Mentions[i] = m
				}
				continue
			}
			mentions[dedupe] = len(g.Mentions)
			g.Mentions = append(g.Mentions, m)
		}
		for _, r := range br.Relations {
			head, tail := locals[r.Head], locals[r.Tail]
			if !head.ok || !tail.ok {
				g.Dropped.UnresolvedEnds++
				continue
			}
			if !s.ValidRelation(r.Type, head.mention.Type, tail.mention.Type) {
				g.Dropped.InvalidRelations++
				continue
			}
			c := claim(snap, block, text, r, head.mention, tail.mention)
			if i, seen := claims[c.ID]; seen {
				g.Dropped.Duplicates++
				if c.HeadScore+c.TailScore > g.Claims[i].HeadScore+g.Claims[i].TailScore {
					g.Claims[i] = c
				}
				continue
			}
			claims[c.ID] = len(g.Claims)
			g.Claims = append(g.Claims, c)
		}
	}
	for _, e := range entities {
		g.Entities = append(g.Entities, e)
	}
	sort.Slice(g.Entities, func(i, j int) bool { return g.Entities[i].ID < g.Entities[j].ID })
	return g, nil
}

func evidence(snap Snapshot, b Block, text []rune, start, end int) Evidence {
	srcStart, srcEnd, exact := SourceRange(b, start, end)
	return Evidence{SourceID: snap.Key.SourceID, Path: snap.Key.Path, SHA: snap.Key.SHA, Fingerprint: snap.Fingerprint, BlockID: b.ID, Quote: string(text[start:end]), Start: start, End: end, SourceStart: srcStart, SourceEnd: srcEnd, Exact: exact}
}

func claim(snap Snapshot, b Block, text []rune, r RelationSpan, head, tail Mention) Claim {
	start := sentenceStart(text, min(head.Evidence.Start, tail.Evidence.Start))
	end := sentenceEnd(text, max(head.Evidence.End, tail.Evidence.End))
	for start < end && unicode.IsSpace(text[start]) {
		start++
	}
	for end > start && unicode.IsSpace(text[end-1]) {
		end--
	}
	ev := evidence(snap, b, text, start, end)
	parts := []string{string(snap.Fingerprint), snap.Key.SourceID, snap.Key.Path, snap.Key.SHA, b.ID, r.Type,
		strconv.Itoa(head.Evidence.Start), strconv.Itoa(head.Evidence.End), strconv.Itoa(tail.Evidence.Start), strconv.Itoa(tail.Evidence.End)}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	c := Claim{ID: "clm-" + hex.EncodeToString(sum[:10]), Relation: r.Type, HeadID: head.EntityID, TailID: tail.EntityID,
		HeadScore: r.HeadConfidence, TailScore: r.TailConfidence, Status: ClaimExtracted, Markers: Markers(ev.Quote), Evidence: ev}
	if r.Confidence != nil {
		v := *r.Confidence
		c.Score = &v
	}
	if len(c.Markers) > 0 {
		c.Status = ClaimUncertain
	}
	return c
}

// Sentence boundaries: a line break, or ". ! ? …" followed by white space.
func sentenceEnder(r rune) bool { return r == '.' || r == '!' || r == '?' || r == '…' }

func sentenceStart(text []rune, pos int) int {
	for p := pos; p > 0; p-- {
		if text[p-1] == '\n' || p >= 2 && unicode.IsSpace(text[p-1]) && sentenceEnder(text[p-2]) {
			return p
		}
	}
	return 0
}

func sentenceEnd(text []rune, pos int) int {
	for k := pos; k < len(text); k++ {
		if text[k] == '\n' {
			return k
		}
		if sentenceEnder(text[k]) && (k+1 == len(text) || unicode.IsSpace(text[k+1])) {
			return k + 1
		}
	}
	return len(text)
}
