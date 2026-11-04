package knowledge_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Mesteriis/veda-wiki/internal/knowledge"
)

// extractorSettings mirrors the pipeline object the extractor reports.
var extractorSettings = json.RawMessage(`{"model_id":"fastino/gliner2.5-multi-v1","model_revision":"cf5593a5d45e3bbf204b9df621b13b1c0cf25ee3","chunker_version":1,"window_tokens":512,"window_margin":16,"overlap_tokens":64,"max_entities_per_block":512,"max_relations_per_block":1024,"decoder":{"optimizer":"beam","beam_size":32,"top_k_entities":32,"max_edges_per_type":256,"candidate_threshold":0.05,"relation_role_threshold":0.05,"top_k_roles":12,"count_top_k":2,"relation_pair_cap":128,"entity_threshold":null,"library":"gliner2==2.1.0"}}`)

func pipeline(s knowledge.Schema, aliases knowledge.Aliases, digest string, settings json.RawMessage) knowledge.Pipeline {
	p, err := knowledge.NewPipeline(s, aliases, digest, settings)
	if err != nil {
		panic(err)
	}
	return p
}

var testPipeline = pipeline(knowledge.SchemaV1(), knowledge.Aliases{}, "", extractorSettings)

func span(text, name string, nth int) (int, int) {
	from := 0
	for {
		i := strings.Index(text[from:], name)
		if i < 0 {
			panic("no " + name)
		}
		if nth == 0 {
			start := utf8.RuneCountInString(text[:from+i])
			return start, start + utf8.RuneCountInString(name)
		}
		nth--
		from += i + len(name)
	}
}

func entitySpan(id, typ, text, name string) knowledge.EntitySpan {
	s, e := span(text, name, 0)
	return knowledge.EntitySpan{LocalID: id, Type: typ, Start: s, End: e, Text: name, Confidence: 0.9}
}

func prepared(t *testing.T, md string) []knowledge.Block {
	t.Helper()
	blocks, err := knowledge.Prepare(md)
	if err != nil {
		t.Fatal(err)
	}
	return blocks
}

func TestBuildGraphMentionsClaimsAndMarkers(t *testing.T) {
	md := "# Сервис 🚀\n\nСервис *billing-api* работает на db1.example. Он не использует Redis.\n"
	blocks := prepared(t, md)
	body := blocks[1]
	snap := knowledge.Snapshot{Key: knowledge.SnapshotKey{SourceID: "ops", Path: "billing.md", SHA: "s1"}, Fingerprint: testPipeline.Fingerprint}
	res := knowledge.ExtractResult{SchemaVersion: 1, Model: knowledge.ModelInfo{ID: knowledge.ModelID, Revision: knowledge.ModelRevision}, Blocks: []knowledge.BlockResult{
		{ID: blocks[0].ID, Chunks: []knowledge.ChunkResult{}, Entities: []knowledge.EntitySpan{}, Relations: []knowledge.RelationSpan{}},
		{ID: body.ID, Chunks: []knowledge.ChunkResult{{Start: 0, End: utf8.RuneCountInString(body.Text), Feasible: true, Status: "ok"}},
			Entities: []knowledge.EntitySpan{
				entitySpan("e1", "service", body.Text, "billing-api"),
				entitySpan("e2", "host", body.Text, "db1.example"),
				entitySpan("e3", "technology", body.Text, "Redis"),
				entitySpan("e4", "host", body.Text, "db1.example"), // overlap duplicate
			},
			Relations: []knowledge.RelationSpan{
				{Type: "runs_on", Head: "e1", Tail: "e2", HeadConfidence: 0.8, TailConfidence: 0.7},
				{Type: "uses", Head: "e1", Tail: "e3", HeadConfidence: 0.8, TailConfidence: 0.6},
				{Type: "runs_on", Head: "e3", Tail: "e2", HeadConfidence: 0.5, TailConfidence: 0.5}, // technology cannot run_on
			},
			Dropped: &knowledge.DroppedCounts{OverLimit: 2}},
	}}
	g, err := knowledge.BuildGraph(snap, knowledge.SchemaV1(), blocks, res, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Mentions) != 3 || g.Dropped.Duplicates != 1 || g.Dropped.InvalidRelations != 1 || g.Dropped.Extractor.OverLimit != 2 {
		t.Fatalf("mentions %d dropped %+v", len(g.Mentions), g.Dropped)
	}
	srcRunes := []rune(md)
	for _, m := range g.Mentions {
		ev := m.Evidence
		if !ev.Exact || string(srcRunes[ev.SourceStart:ev.SourceEnd]) != m.Text || ev.Quote != m.Text || ev.Fingerprint != snap.Fingerprint {
			t.Fatalf("evidence does not map to source: %+v", ev)
		}
		if m.Type == "host" && m.Resolution.Method != knowledge.ResolveTechnicalKey {
			t.Fatalf("host resolution %+v", m.Resolution)
		}
	}
	if len(g.Claims) != 2 {
		t.Fatalf("claims %+v", g.Claims)
	}
	for _, c := range g.Claims {
		switch c.Relation {
		case "runs_on":
			if c.Status != knowledge.ClaimExtracted || !strings.HasPrefix(c.Evidence.Quote, "Сервис") || strings.Contains(c.Evidence.Quote, "Redis") {
				t.Fatalf("runs_on claim %+v", c)
			}
		case "uses":
			// The sentences from billing-api to Redis hold the negation "не".
			if c.Status != knowledge.ClaimUncertain || len(c.Markers) == 0 || c.Markers[0] != "negation:не" {
				t.Fatalf("uses claim %+v", c)
			}
		}
	}
	again, _ := knowledge.BuildGraph(snap, knowledge.SchemaV1(), blocks, res, nil)
	if again.Claims[0].ID != g.Claims[0].ID || again.Entities[0].ID != g.Entities[0].ID {
		t.Fatal("graph IDs are not deterministic")
	}
}

func TestBuildGraphAliasesAndSourceScopedNames(t *testing.T) {
	blocks := prepared(t, "Иван Петров uses pg.\n")
	b := blocks[0]
	res := knowledge.ExtractResult{SchemaVersion: 1, Blocks: []knowledge.BlockResult{{ID: b.ID, Entities: []knowledge.EntitySpan{
		entitySpan("p", "person", b.Text, "Иван Петров"), entitySpan("t", "technology", b.Text, "pg"),
	}}}}
	aliases, digest, err := knowledge.ParseAliases([]byte(`{"version":1,"aliases":{"technology":{"pg":"PostgreSQL"}}}`), knowledge.SchemaV1())
	if err != nil || digest == "" {
		t.Fatal(err)
	}
	build := func(source string) knowledge.Graph {
		g, err := knowledge.BuildGraph(knowledge.Snapshot{Key: knowledge.SnapshotKey{SourceID: source, Path: "a.md", SHA: "x"}}, knowledge.SchemaV1(), blocks, res, aliases)
		if err != nil {
			t.Fatal(err)
		}
		return g
	}
	a, b2 := build("one"), build("two")
	ids := func(g knowledge.Graph, typ string) string {
		for _, m := range g.Mentions {
			if m.Type == typ {
				return m.EntityID
			}
		}
		return ""
	}
	if ids(a, "person") == ids(b2, "person") {
		t.Fatal("a person name merged across sources")
	}
	if ids(a, "technology") != ids(b2, "technology") || ids(a, "technology") != knowledge.EntityID("technology:postgresql") {
		t.Fatal("explicit alias did not merge")
	}
	if pipeline(knowledge.SchemaV1(), aliases, digest, extractorSettings).Fingerprint == testPipeline.Fingerprint {
		t.Fatal("aliases do not change the fingerprint")
	}
	if pipeline(knowledge.SchemaV1(), aliases, digest, extractorSettings).Extraction != testPipeline.Extraction {
		t.Fatal("aliases changed the extraction identity")
	}
	if _, _, err = knowledge.ParseAliases([]byte(`{"version":1,"aliases":{"planet":{"a":"b"}}}`), knowledge.SchemaV1()); err == nil {
		t.Fatal("unknown alias type accepted")
	}
}

func TestValidateResultRejectsMismatchedSpans(t *testing.T) {
	req := knowledge.ExtractRequest{SchemaVersion: 1, RequestID: "r1", Schema: knowledge.WireSchemaOf(knowledge.SchemaV1()), Blocks: []knowledge.WireBlock{{ID: "b1", Text: "Ёж 🦔 uses Go"}}}
	good := knowledge.EntitySpan{LocalID: "e1", Type: "technology", Start: 10, End: 12, Text: "Go", Confidence: 0.5}
	base := func(e knowledge.EntitySpan) knowledge.ExtractResult {
		return knowledge.ExtractResult{SchemaVersion: 1, RequestID: "r1", Blocks: []knowledge.BlockResult{{ID: "b1", Entities: []knowledge.EntitySpan{e}}}}
	}
	if err := knowledge.ValidateResult(req, base(good)); err != nil {
		t.Fatal("code-point span rejected:", err)
	}
	cases := map[string]knowledge.ExtractResult{}
	bytesSpan := good
	bytesSpan.Start, bytesSpan.End = 13, 15 // UTF-8 byte offsets
	cases["byte offsets"] = base(bytesSpan)
	wrongText := good
	wrongText.Text = "Gx"
	cases["text mismatch"] = base(wrongText)
	badType := good
	badType.Type = "planet"
	cases["undeclared type"] = base(badType)
	nan := good
	nan.Confidence = 2
	cases["score out of range"] = base(nan)
	echo := base(good)
	echo.RequestID = "other"
	cases["request id"] = echo
	missing := base(good)
	missing.Blocks = nil
	cases["missing block"] = missing
	dangling := base(good)
	dangling.Blocks[0].Relations = []knowledge.RelationSpan{{Type: "uses", Head: "e1", Tail: "e9"}}
	cases["dangling relation"] = dangling
	for name, res := range cases {
		if err := knowledge.ValidateResult(req, res); !errors.Is(err, knowledge.ErrInvalidResponse) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestRequestsBatchAndRejectHugeBlocks(t *testing.T) {
	snap := knowledge.Snapshot{Key: knowledge.SnapshotKey{SourceID: "s", Path: "p.md", SHA: "x"}, Fingerprint: testPipeline.Fingerprint}
	var blocks []knowledge.Block
	for i := range knowledge.MaxRequestBlocks + 3 {
		blocks = append(blocks, knowledge.Block{ID: "b" + string(rune('a'+i%26)) + strings.Repeat("x", i), Text: "text"})
	}
	reqs, err := knowledge.Requests(snap, knowledge.SchemaV1(), blocks)
	if err != nil || len(reqs) != 2 || len(reqs[0].Blocks) != knowledge.MaxRequestBlocks || reqs[0].RequestID == reqs[1].RequestID {
		t.Fatalf("batches %d %v", len(reqs), err)
	}
	if _, err = knowledge.Requests(snap, knowledge.SchemaV1(), []knowledge.Block{{ID: "b", Text: strings.Repeat("я", knowledge.MaxBlockRunes+1)}}); !errors.Is(err, knowledge.ErrBlockTooLarge) {
		t.Fatal("huge block accepted", err)
	}
	reordered := json.RawMessage(`{"window_margin":16,"model_id":"fastino/gliner2.5-multi-v1","model_revision":"cf5593a5d45e3bbf204b9df621b13b1c0cf25ee3","chunker_version":1,"window_tokens":512,"overlap_tokens":64,"max_entities_per_block":512,"max_relations_per_block":1024,"decoder":{"library":"gliner2==2.1.0","optimizer":"beam","beam_size":32,"top_k_entities":32,"max_edges_per_type":256,"candidate_threshold":0.05,"relation_role_threshold":0.05,"top_k_roles":12,"count_top_k":2,"relation_pair_cap":128,"entity_threshold":null}}`)
	if pipeline(knowledge.SchemaV1(), nil, "", reordered).Fingerprint != testPipeline.Fingerprint {
		t.Fatal("key order changed the fingerprint")
	}
	greedy := json.RawMessage(strings.Replace(string(extractorSettings), `"beam"`, `"greedy"`, 1))
	if pipeline(knowledge.SchemaV1(), nil, "", greedy).Fingerprint == testPipeline.Fingerprint {
		t.Fatal("decoder settings do not enter the fingerprint")
	}
	for _, pair := range [][2]string{{`{"a":0.05,"b":1}`, `{"b":1.0,"a":5e-2}`}, {`{"a":[1,2.50]}`, `{"a":[1e0,2.5]}`}, {`{"a":-0.0}`, `{"a":-0}`}} {
		x, errX := knowledge.ExtractorDigest(json.RawMessage(pair[0]))
		y, errY := knowledge.ExtractorDigest(json.RawMessage(pair[1]))
		if errX != nil || errY != nil || x != y {
			t.Fatalf("%s and %s digest differently", pair[0], pair[1])
		}
	}
	if x, _ := knowledge.ExtractorDigest(json.RawMessage(`{"a":0.05}`)); x == func() string { y, _ := knowledge.ExtractorDigest(json.RawMessage(`{"a":0.5}`)); return y }() {
		t.Fatal("different numbers share a digest")
	}
	for _, bad := range []string{`{"a":1e999}`, `{"a":1} {}`, `null`} {
		if _, err = knowledge.ExtractorDigest(json.RawMessage(bad)); err == nil {
			t.Fatalf("%s accepted", bad)
		}
	}
	if _, err = knowledge.NewPipeline(knowledge.SchemaV1(), nil, "", json.RawMessage(`[1]`)); err == nil {
		t.Fatal("non-object settings accepted")
	}
	if knowledge.Backoff(1) != knowledge.BackoffBase || knowledge.Backoff(20) != knowledge.BackoffMax {
		t.Fatal("backoff bounds")
	}
}
