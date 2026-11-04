package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

// ErrNotFound reports an entity or snapshot the request may not read or
// that the index does not hold; callers answer both with not found.
var ErrNotFound = errors.New("knowledge: not found")

// Index is the derived knowledge index. The worker publishes and retires
// snapshots; reconciliation lists them. Every read takes the request's
// AllowedSet and the active fingerprint and is computed only from evidence
// of snapshots in that set under that fingerprint: names, aliases, counts,
// ranking, candidates, related-article reasons and traversal never use a
// snapshot outside it, so a hidden document cannot change any answer.
type Index interface {
	// Publish replaces the graph of snap as one unit: a reader sees either
	// the previous complete graph of snap or the new one. Publishing the
	// same snapshot again creates no duplicates.
	Publish(ctx context.Context, snap Snapshot, g Graph) error
	// Retire removes the graphs of snaps. Entities stay while another
	// snapshot still refers to them. Unknown snapshots are ignored.
	Retire(ctx context.Context, snaps []Snapshot) error
	// Snapshots lists the published snapshots.
	Snapshots(ctx context.Context) ([]Snapshot, error)
	// Status reports whether the index is reachable.
	Status(ctx context.Context) error

	// Entities searches entities by name or alias, ranked by the number of
	// allowed documents that mention them.
	Entities(ctx context.Context, allowed AllowedSet, fp Fingerprint, q EntityQuery) (EntityPage, error)
	// Entity is one entity with its allowed mentions, claims, documents and
	// candidate matches. ErrNotFound when no allowed evidence mentions it.
	Entity(ctx context.Context, allowed AllowedSet, fp Fingerprint, id string, limit int) (EntityCard, error)
	// Article is the knowledge of one allowed snapshot: its entities,
	// mentions, claims and related articles. ErrNotFound when key is not
	// allowed or not published under fp.
	Article(ctx context.Context, allowed AllowedSet, fp Fingerprint, key SnapshotKey, limit int) (ArticleView, error)
	// Graph is a bounded subgraph around an entity or a snapshot.
	Graph(ctx context.Context, allowed AllowedSet, fp Fingerprint, q GraphQuery) (Subgraph, error)
	// Context selects evidence for a query: entities matching the text,
	// lexical hits passed in by the caller (Seeds), and claims reached
	// from them within Depth, up to LimitBytes.
	Context(ctx context.Context, allowed AllowedSet, fp Fingerprint, q ContextQuery) (ContextResult, error)
}

// Read limits shared by every Index implementation.
const (
	DefaultLimit = 50
	MaxLimit     = 200
	// MaxEvidencePerReason bounds the quotes attached to one reason or edge.
	MaxEvidencePerReason = 3
	// Generic entities do not relate articles: an entity mentioned in more
	// than GenericShare of the allowed documents, and in at least
	// GenericMinDocs of them, is common vocabulary ("API", "server") and is
	// not by itself a reason that two articles are related.
	GenericShare   = 0.25
	GenericMinDocs = 5
	// DefaultContextBytes and MaxContextBytes bound a context answer.
	DefaultContextBytes = 16 << 10
	MaxContextBytes     = 64 << 10
)

// ClampLimit returns limit within [1, MaxLimit], DefaultLimit when unset.
func ClampLimit(limit int) int {
	switch {
	case limit <= 0:
		return DefaultLimit
	case limit > MaxLimit:
		return MaxLimit
	}
	return limit
}

// EntityQuery searches entities. Text matches a normalized name or alias by
// substring; Type and SourceID filter; Offset pages.
type EntityQuery struct {
	Text     string
	Type     string
	SourceID string
	Limit    int
	Offset   int
}

// EntitySummary is an entity with counts over allowed evidence only.
type EntitySummary struct {
	Entity
	Mentions  int `json:"mentions"`
	Documents int `json:"documents"`
}

// EntityPage is one page of entities; Next is the next offset, or 0.
type EntityPage struct {
	Entities []EntitySummary `json:"entities"`
	Total    int             `json:"total"`
	Next     int             `json:"next,omitempty"`
}

// Candidate is a suggested, unmerged match: another allowed entity of the
// same type whose normalized name is equal but whose key is scoped to
// another source.
type Candidate struct {
	EntityID   string     `json:"entity_id"`
	Name       string     `json:"name"`
	Resolution Resolution `json:"resolution"`
}

// EntityCard is the detail of one entity.
type EntityCard struct {
	EntitySummary
	MentionList  []Mention     `json:"mention_list"`
	Claims       []Claim       `json:"claims"`
	DocumentList []SnapshotKey `json:"document_list"`
	Candidates   []Candidate   `json:"candidates"`
	Truncated    bool          `json:"truncated"`
}

// ArticleView is the knowledge of one snapshot.
type ArticleView struct {
	Key       SnapshotKey      `json:"key"`
	Entities  []EntitySummary  `json:"entities"`
	Mentions  []Mention        `json:"mentions"`
	Claims    []Claim          `json:"claims"`
	Related   []RelatedArticle `json:"related"`
	Truncated bool             `json:"truncated"`
}

// GraphCenter is where a subgraph starts: an entity or one snapshot.
type GraphCenter struct {
	EntityID string
	Snapshot *SnapshotKey
}

// GraphQuery bounds a subgraph: Depth hops (DepthDefault when 0, at most
// DepthMax), optional source and relation filters, node and edge caps
// (schema limits when 0, never above them).
type GraphQuery struct {
	Center    GraphCenter
	Depth     int
	SourceIDs []string
	Relations []string
	MaxNodes  int
	MaxEdges  int
}

// Node kinds and edge kinds of a subgraph.
const (
	NodeEntity   = "entity"
	NodeDocument = "document"
	EdgeMention  = "mention"
	EdgeRelation = "relation"
)

// Node is an entity or a document snapshot.
type Node struct {
	ID    string       `json:"id"`
	Kind  string       `json:"kind"`
	Type  string       `json:"type,omitempty"`
	Label string       `json:"label"`
	Key   *SnapshotKey `json:"key,omitempty"`
}

// Edge is a mention (document to entity, Count mentions) or one extracted
// claim (head entity to tail entity). Independent claims stay separate edges.
type Edge struct {
	ID       string      `json:"id"`
	Kind     string      `json:"kind"`
	From     string      `json:"from"`
	To       string      `json:"to"`
	Relation string      `json:"relation,omitempty"`
	Status   ClaimStatus `json:"status,omitempty"`
	Markers  []string    `json:"markers,omitempty"`
	Count    int         `json:"count,omitempty"`
	Evidence []Evidence  `json:"evidence"`
}

// Subgraph is a bounded graph answer; Truncated reports hit caps.
type Subgraph struct {
	Nodes     []Node `json:"nodes"`
	Edges     []Edge `json:"edges"`
	Truncated bool   `json:"truncated"`
}

// DocumentNodeID is the node ID of a snapshot in a subgraph.
func DocumentNodeID(k SnapshotKey) string {
	sum := sha256.Sum256([]byte(k.SourceID + "\x00" + k.Path + "\x00" + k.SHA))
	return "doc-" + hex.EncodeToString(sum[:10])
}

// ContextQuery asks for graph context. Seeds are lexical search hits the
// caller found with VEDA's search over the same request snapshot; they are
// filtered by the allowed set again.
type ContextQuery struct {
	Text       string
	Seeds      []SnapshotKey
	Depth      int
	LimitBytes int
	MaxItems   int
}

// Context item kinds and selection reasons.
const (
	ItemEntity   = "entity"
	ItemClaim    = "claim"
	ItemDocument = "document"

	ReasonEntityMatch     = "entity_match"
	ReasonLexicalHit      = "lexical_hit"
	ReasonMentionedInHit  = "mentioned_in_hit"
	ReasonRelatedToEntity = "relation_of_selected_entity"
)

// ContextItem is one selected piece of evidence with the reason it was
// selected. Text is untrusted article data.
type ContextItem struct {
	Kind     string       `json:"kind"`
	Reason   string       `json:"reason"`
	Depth    int          `json:"depth"`
	Entity   *Entity      `json:"entity,omitempty"`
	Claim    *Claim       `json:"claim,omitempty"`
	Key      *SnapshotKey `json:"key,omitempty"`
	Evidence []Evidence   `json:"evidence"`
}

// ContextResult is the selected context; Bytes is its JSON size.
type ContextResult struct {
	Items     []ContextItem `json:"items"`
	Bytes     int           `json:"bytes"`
	Truncated bool          `json:"truncated"`
}
