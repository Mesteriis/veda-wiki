package knowledge

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// AliasesFile is the owner's alias configuration, kept in the data
// directory next to the index and never removed by a reindex:
//
//	{"version": 1, "aliases": {"service": {"pg": "postgres"}}}
//
// Each entry maps an alias to the canonical name of the same entity type.
const AliasesFile = "knowledge-aliases.json"

// AliasesVersion is the only supported format version of AliasesFile.
const AliasesVersion = 1

type aliasesDoc struct {
	Version int                          `json:"version"`
	Aliases map[string]map[string]string `json:"aliases"`
}

// ParseAliases decodes AliasesFile strictly and returns the rules keyed by
// normalized alias, and a digest of the effective rules that enters the
// pipeline fingerprint (an alias change re-resolves every snapshot). Types
// must be declared in s; aliases and names must not be empty.
func ParseAliases(data []byte, s Schema) (Aliases, string, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var doc aliasesDoc
	if err := dec.Decode(&doc); err != nil {
		return nil, "", fmt.Errorf("knowledge aliases: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, "", errors.New("knowledge aliases: trailing data")
	}
	if doc.Version != AliasesVersion {
		return nil, "", fmt.Errorf("knowledge aliases: unsupported version %d", doc.Version)
	}
	out := Aliases{}
	for typ, rules := range doc.Aliases {
		if !s.HasEntity(typ) {
			return nil, "", fmt.Errorf("knowledge aliases: unknown entity type %q", typ)
		}
		out[typ] = map[string]string{}
		for alias, canonical := range rules {
			k, _ := Normalize(typ, alias)
			ck, _ := Normalize(typ, canonical)
			if k == "" || ck == "" {
				return nil, "", fmt.Errorf("knowledge aliases: empty alias or name for %q", typ)
			}
			out[typ][k] = canonical
		}
	}
	// encoding/json sorts map keys, so equal rules give equal digests.
	canonical, err := json.Marshal(out)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(canonical)
	return out, "aliases:" + hex.EncodeToString(sum[:]), nil
}
