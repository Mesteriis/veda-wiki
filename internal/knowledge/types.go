// Package knowledge holds the contracts of the derived knowledge graph: text
// preparation with source maps, the per-request access rule over VEDA's own
// read rules, the extraction schema, pipeline fingerprints, entity
// normalization and uncertainty markers. These contracts are pure: no I/O,
// no clock reads. The package also holds the background indexing Service
// (reconcile.go, worker.go, publish.go), which reaches the store, the job
// database, the extractor and the index only through the Snapshots,
// Manifest, Extractor and Index interfaces and takes its clock from Config.
//
// Every coordinate is a half-open range [start, end) of Unicode code points,
// in the block text (Start, End) or in the original Markdown (SourceStart,
// SourceEnd).
package knowledge

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
)

// Versions of the Go stages of the pipeline. A change in what Prepare emits,
// in Normalize or in Markers must bump the matching version, which changes
// the pipeline fingerprint and causes reprocessing.
const (
	PreprocessVersion = "prepare-v2"
	NormalizeVersion  = "normalize-v3"
	ResolveVersion    = NormalizeVersion + "+" + MarkersVersion
)

// SnapshotKey identifies one immutable text of an article: the article is
// SourceID and Path, the text is its SHA-256. Revision numbers are not part
// of the identity.
type SnapshotKey struct {
	SourceID string `json:"source_id"`
	Path     string `json:"path"`
	SHA      string `json:"content_sha256"`
}

// Article is the store key of the snapshot's article.
func (k SnapshotKey) Article() string { return k.SourceID + "/" + k.Path }

// Fingerprint identifies the extraction pipeline: schema, preprocessing,
// resolution rules and model checkpoint.
type Fingerprint string

// PipelineFingerprint derives the fingerprint from its parts. Parts are
// length-delimited, so no two different part lists share a fingerprint.
func PipelineFingerprint(schema, preprocessVersion, resolveVersion, checkpoint string) Fingerprint {
	h := sha256.New()
	for _, part := range []string{schema, preprocessVersion, resolveVersion, checkpoint} {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(part)))
		h.Write(n[:])
		h.Write([]byte(part))
	}
	return Fingerprint("kg1-" + hex.EncodeToString(h.Sum(nil)))
}

// Block kinds emitted by Prepare.
const (
	BlockHeading   = "heading"
	BlockParagraph = "paragraph"
	BlockListItem  = "list_item"
	BlockTableCell = "table_cell"
)

// Block is one unit of prepared text. Map lists the verbatim pieces of Text,
// in order: Text[Start:End] equals the Markdown [SrcStart:SrcEnd]. Characters
// of Text outside every span were inserted by Prepare (line breaks).
type Block struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Text string `json:"text"`
	Map  []Span `json:"map"`
}

// Span maps a block range to the source range holding the same text.
type Span struct {
	Start    int `json:"start"`
	End      int `json:"end"`
	SrcStart int `json:"source_start"`
	SrcEnd   int `json:"source_end"`
}

// Evidence is the provenance of a mention or claim: the snapshot, the block
// and the quoted range in block and source coordinates. Exact reports that
// the source range holds exactly Quote; otherwise the source range only
// encloses it and must not be highlighted as a verbatim match.
type Evidence struct {
	SourceID    string      `json:"source_id"`
	Path        string      `json:"path"`
	SHA         string      `json:"content_sha256"`
	Fingerprint Fingerprint `json:"pipeline_fingerprint"`
	BlockID     string      `json:"block_id"`
	Quote       string      `json:"quote"`
	Start       int         `json:"start"`
	End         int         `json:"end"`
	SourceStart int         `json:"source_start"`
	SourceEnd   int         `json:"source_end"`
	Exact       bool        `json:"exact"`
}

// Key is the snapshot the evidence comes from.
func (e Evidence) Key() SnapshotKey {
	return SnapshotKey{SourceID: e.SourceID, Path: e.Path, SHA: e.SHA}
}

// ResolutionMethod is the rule that tied a mention to an entity.
type ResolutionMethod string

// Resolution methods, from strongest to weakest. A candidate is a suggested
// match only; it never merges entities.
const (
	ResolveTechnicalKey   ResolutionMethod = "technical_key"
	ResolveAlias          ResolutionMethod = "alias"
	ResolveSameSourceName ResolutionMethod = "same_source_name"
	ResolveCandidate      ResolutionMethod = "candidate"
)

// Resolution records the method and its basis: the technical key, the alias
// rule, the source or the shared normalized name.
type Resolution struct {
	Method ResolutionMethod `json:"method"`
	Basis  string           `json:"basis,omitempty"`
}

// Entity is a canonical entity. Its name and aliases come only from evidence
// the reader is allowed to see.
type Entity struct {
	ID         string     `json:"id"`
	Type       string     `json:"type"`
	Name       string     `json:"name"`
	Key        string     `json:"key"`
	Aliases    []string   `json:"aliases,omitempty"`
	Resolution Resolution `json:"resolution"`
}

// Mention is one occurrence of an entity in a block. Score is the raw model
// score of the span, not a probability that the text is true.
type Mention struct {
	EntityID   string     `json:"entity_id"`
	Type       string     `json:"type"`
	Text       string     `json:"text"`
	Score      float64    `json:"score"`
	Resolution Resolution `json:"resolution"`
	Evidence   Evidence   `json:"evidence"`
}

// ClaimStatus says how far an extracted relation can be taken at face value.
type ClaimStatus string

// A claim is a hypothesis with a source. It is uncertain when its sentence
// has a negation, plan, example or past marker, listed in Claim.Markers.
const (
	ClaimExtracted ClaimStatus = "extracted"
	ClaimUncertain ClaimStatus = "uncertain"
)

// Claim is one extracted, typed relation from one block. Scores are raw model
// scores: Score is the relation's own score when the extraction path gives
// one, HeadScore and TailScore those of its ends.
type Claim struct {
	ID        string      `json:"id"`
	Relation  string      `json:"relation"`
	HeadID    string      `json:"head_id"`
	TailID    string      `json:"tail_id"`
	Score     *float64    `json:"score,omitempty"`
	HeadScore float64     `json:"head_score"`
	TailScore float64     `json:"tail_score"`
	Status    ClaimStatus `json:"status"`
	Markers   []string    `json:"markers,omitempty"`
	Evidence  Evidence    `json:"evidence"`
}

// ReasonKind distinguishes why two articles are related. A shared entity is
// co-mention, never a typed dependency.
type ReasonKind string

// Reason kinds: an explicit Markdown link, a shared allowed entity, an
// extracted typed relation and a suggested (candidate) entity match.
const (
	ReasonLink         ReasonKind = "link"
	ReasonSharedEntity ReasonKind = "shared_entity"
	ReasonRelation     ReasonKind = "relation"
	ReasonCandidate    ReasonKind = "candidate"
)

// Reason explains one relation between articles with its supporting evidence.
type Reason struct {
	Kind     ReasonKind `json:"kind"`
	EntityID string     `json:"entity_id,omitempty"`
	ClaimID  string     `json:"claim_id,omitempty"`
	Relation string     `json:"relation,omitempty"`
	Evidence []Evidence `json:"evidence,omitempty"`
}

// RelatedArticle is an article related to another, with every reason.
type RelatedArticle struct {
	Key     SnapshotKey `json:"key"`
	Reasons []Reason    `json:"reasons"`
}
