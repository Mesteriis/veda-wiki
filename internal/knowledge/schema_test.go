package knowledge

import (
	"strings"
	"testing"
)

func TestSchemaV1(t *testing.T) {
	s := SchemaV1()
	if s.Version != "v1" || len(s.Entities) != 8 || len(s.Relations) != 6 {
		t.Fatalf("schema v1: %s %d entities %d relations", s.Version, len(s.Entities), len(s.Relations))
	}
	for _, typ := range []string{"person", "organization", "project", "service", "host", "repository", "technology", "document"} {
		if !s.HasEntity(typ) {
			t.Fatalf("missing entity %s", typ)
		}
	}
	if s.HasEntity("Person") || s.HasEntity("city") {
		t.Fatal("unknown entity type accepted")
	}
	valid := [][3]string{
		{"depends_on", "service", "technology"}, {"depends_on", "project", "repository"},
		{"runs_on", "service", "host"}, {"uses", "person", "technology"},
		{"part_of", "host", "organization"}, {"maintained_by", "repository", "person"},
		{"documents", "document", "host"},
	}
	for _, v := range valid {
		if !s.ValidRelation(v[0], v[1], v[2]) {
			t.Fatalf("%v rejected", v)
		}
	}
	invalid := [][3]string{
		{"runs_on", "host", "service"}, {"depends_on", "person", "service"},
		{"documents", "service", "document"}, {"knows", "person", "person"},
		{"uses", "technology", "service"}, {"part_of", "person", "project"},
	}
	for _, v := range invalid {
		if s.ValidRelation(v[0], v[1], v[2]) {
			t.Fatalf("%v accepted", v)
		}
	}
	if s.Limits.DepthMax != 2 || s.Limits.NodesMax != 200 || s.Limits.EdgesMax != 400 || s.Limits.DepthDefault != 1 {
		t.Fatalf("limits %+v", s.Limits)
	}
	if !strings.HasPrefix(s.Digest, "sha256:") || len(SchemaV1JSON()) == 0 {
		t.Fatal("schema digest or bytes missing")
	}
}

func TestParseSchemaRejectsInconsistentSchemas(t *testing.T) {
	limits := `"limits":{"depth_default":1,"depth_max":2,"nodes_max":200,"edges_max":400,"request_bytes_max":1,"blocks_max":1}`
	bad := map[string]string{
		"unknown endpoint": `{"version":"v1","entities":[{"type":"host"}],"relations":[{"type":"runs_on","head":["service"],"tail":["host"]}],` + limits + `}`,
		"duplicate entity": `{"version":"v1","entities":[{"type":"host"},{"type":"host"}],"relations":[],` + limits + `}`,
		"unknown field":    `{"version":"v1","entities":[],"relations":[],"extra":1,` + limits + `}`,
		"no version":       `{"entities":[],"relations":[],` + limits + `}`,
		"empty ends":       `{"version":"v1","entities":[{"type":"host"}],"relations":[{"type":"r","head":[],"tail":["host"]}],` + limits + `}`,
		"bad limits":       `{"version":"v1","entities":[],"relations":[],"limits":{"depth_default":3,"depth_max":2,"nodes_max":1,"edges_max":1,"request_bytes_max":1,"blocks_max":1}}`,
		"trailing data":    `{"version":"v1","entities":[],"relations":[],` + limits + `} {}`,
	}
	for name, data := range bad {
		if _, err := ParseSchema([]byte(data)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := ParseSchema(SchemaV1JSON()); err != nil {
		t.Fatal(err)
	}
}

func TestPipelineFingerprintChangesWithEveryPart(t *testing.T) {
	base := PipelineFingerprint("s", "p", "r", "c")
	if base != PipelineFingerprint("s", "p", "r", "c") || !strings.HasPrefix(string(base), "kg1-") {
		t.Fatalf("fingerprint %q not stable", base)
	}
	for _, other := range []Fingerprint{
		PipelineFingerprint("s2", "p", "r", "c"), PipelineFingerprint("s", "p2", "r", "c"),
		PipelineFingerprint("s", "p", "r2", "c"), PipelineFingerprint("s", "p", "r", "c2"),
		PipelineFingerprint("sp", "", "r", "c"), // fields are length-delimited
	} {
		if other == base {
			t.Fatal("fingerprint ignores a part")
		}
	}
}
