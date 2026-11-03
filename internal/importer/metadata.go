package importer

import (
	"bytes"
	"io"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// ParseMetadata strictly decodes veda.yaml: bounded UTF-8 without credentials,
// no anchors, aliases or unknown fields, and exactly one document.
func ParseMetadata(b []byte) (*domain.WikiMetadata, error) {
	if len(b) > 16384 || !utf8.Valid(b) || domain.CredentialLikeBytes(b) {
		return nil, domain.ErrInvalid
	}
	var node yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(b))
	if e := dec.Decode(&node); e != nil {
		return nil, domain.ErrInvalid
	}
	var check func(*yaml.Node) bool
	check = func(n *yaml.Node) bool {
		if n.Alias != nil || n.Anchor != "" || (n.Tag != "" && n.Tag != "!!map" && n.Tag != "!!str" && n.Tag != "!!int") {
			return false
		}
		for _, c := range n.Content {
			if !check(c) {
				return false
			}
		}
		return true
	}
	if !check(&node) {
		return nil, domain.ErrInvalid
	}
	decoder := yaml.NewDecoder(bytes.NewReader(b))
	decoder.KnownFields(true)
	var m domain.WikiMetadata
	if e := decoder.Decode(&m); e != nil {
		return nil, domain.ErrInvalid
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, domain.ErrInvalid
	}
	if e := domain.ValidateMetadata(&m); e != nil {
		return nil, e
	}
	return &m, nil
}
