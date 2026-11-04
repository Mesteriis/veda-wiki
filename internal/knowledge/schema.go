package knowledge

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
)

//go:embed schema/v1.json
var schemaV1JSON []byte

var schemaV1 = mustParseSchema(schemaV1JSON)

// Schema is a versioned extraction schema: entity types, relation types with
// the entity types allowed at each end, and service limits. Digest identifies
// the exact schema bytes and enters the pipeline fingerprint.
type Schema struct {
	Version   string         `json:"version"`
	Entities  []EntityType   `json:"entities"`
	Relations []RelationType `json:"relations"`
	Limits    Limits         `json:"limits"`
	Digest    string         `json:"-"`
}

// EntityType is one entity label with its description for the model.
type EntityType struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

// RelationType is one relation label and the entity types of its ends.
type RelationType struct {
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	Head        []string `json:"head"`
	Tail        []string `json:"tail"`
}

// Limits bound graph reads and extraction requests.
type Limits struct {
	DepthDefault    int `json:"depth_default"`
	DepthMax        int `json:"depth_max"`
	NodesMax        int `json:"nodes_max"`
	EdgesMax        int `json:"edges_max"`
	RequestBytesMax int `json:"request_bytes_max"`
	BlocksMax       int `json:"blocks_max"`
}

// SchemaV1 is the embedded schema v1.
func SchemaV1() Schema { return schemaV1 }

// SchemaV1JSON is a copy of the embedded schema bytes, as sent to the
// extractor.
func SchemaV1JSON() []byte { return bytes.Clone(schemaV1JSON) }

func mustParseSchema(data []byte) Schema {
	s, err := ParseSchema(data)
	if err != nil {
		panic(err)
	}
	return s
}

// ParseSchema decodes a schema strictly and checks it: a version, unique
// entity and relation types, non-empty relation ends that name declared
// entity types, and positive, consistent limits.
func ParseSchema(data []byte) (Schema, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var s Schema
	if err := dec.Decode(&s); err != nil {
		return Schema{}, fmt.Errorf("knowledge schema: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return Schema{}, errors.New("knowledge schema: trailing data")
	}
	if s.Version == "" {
		return Schema{}, errors.New("knowledge schema: no version")
	}
	entities := map[string]bool{}
	for _, e := range s.Entities {
		if e.Type == "" || entities[e.Type] {
			return Schema{}, fmt.Errorf("knowledge schema: empty or duplicate entity %q", e.Type)
		}
		entities[e.Type] = true
	}
	relations := map[string]bool{}
	for _, r := range s.Relations {
		if r.Type == "" || relations[r.Type] || len(r.Head) == 0 || len(r.Tail) == 0 {
			return Schema{}, fmt.Errorf("knowledge schema: invalid relation %q", r.Type)
		}
		relations[r.Type] = true
		for _, end := range append(slices.Clone(r.Head), r.Tail...) {
			if !entities[end] {
				return Schema{}, fmt.Errorf("knowledge schema: relation %q names unknown entity %q", r.Type, end)
			}
		}
	}
	l := s.Limits
	if l.DepthDefault < 1 || l.DepthMax < l.DepthDefault || l.NodesMax < 1 || l.EdgesMax < 1 || l.RequestBytesMax < 1 || l.BlocksMax < 1 {
		return Schema{}, errors.New("knowledge schema: invalid limits")
	}
	sum := sha256.Sum256(data)
	s.Digest = "sha256:" + hex.EncodeToString(sum[:])
	return s, nil
}

// HasEntity reports whether typ is a declared entity type.
func (s Schema) HasEntity(typ string) bool {
	return slices.ContainsFunc(s.Entities, func(e EntityType) bool { return e.Type == typ })
}

// ValidRelation reports whether relation is declared and allows head and
// tail entity types at its ends. Model output is checked with it: the model
// does not enforce end types.
func (s Schema) ValidRelation(relation, head, tail string) bool {
	for _, r := range s.Relations {
		if r.Type == relation {
			return slices.Contains(r.Head, head) && slices.Contains(r.Tail, tail)
		}
	}
	return false
}
