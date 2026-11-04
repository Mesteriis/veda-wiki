package knowledge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"unicode/utf8"
)

// Wire contract v1 of the extractor service (POST /v1/extract). Offsets in a
// response are half-open code-point ranges in the text of the request block.
const ExtractSchemaVersion = 1

// The model checkpoint the pipeline is pinned to. A response from any other
// checkpoint is refused, so one fingerprint never mixes models. Every other
// extractor setting that changes output (chunker, window, decoder) is
// reported by the service as model.pipeline and enters the fingerprint
// through ExtractorDigest.
const (
	ModelID       = "fastino/gliner2.5-multi-v1"
	ModelRevision = "cf5593a5d45e3bbf204b9df621b13b1c0cf25ee3"
	Checkpoint    = ModelID + "@" + ModelRevision
)

// Chunk statuses reported by the extractor.
const (
	ChunkOK                 = "ok"
	ChunkInfeasible         = "infeasible"
	ChunkTruncatedPrevented = "truncated_prevented"
)

// Errors an Extractor returns, wrapped. ErrUnavailable is transient (the
// service is down, busy, not ready or unauthorized): the job is retried
// without spending an attempt. ErrRejected is permanent for this input.
// ErrInvalidResponse is a broken response and spends an attempt.
// ErrTimeout (the call or the service ran out of time) and
// ErrExtractorError (a 5xx failure of the service) spend an attempt too.
// ErrBusy (503 busy: the service is up but its inference slot stayed taken)
// is retried shortly without spending an attempt and without degrading.
var (
	ErrUnavailable     = errors.New("knowledge: extractor unavailable")
	ErrBusy            = errors.New("knowledge: extractor busy")
	ErrRejected        = errors.New("knowledge: extractor rejected the request")
	ErrInvalidResponse = errors.New("knowledge: invalid extractor response")
	ErrTimeout         = errors.New("knowledge: extraction timed out")
	ErrExtractorError  = errors.New("knowledge: extractor failed")
)

// Extractor extracts entities and relations from prepared blocks.
// Ready reports the loaded model, including its pipeline settings.
type Extractor interface {
	Extract(ctx context.Context, req ExtractRequest) (ExtractResult, error)
	Ready(ctx context.Context) (ModelInfo, error)
}

// ExtractRequest is the body of POST /v1/extract.
type ExtractRequest struct {
	SchemaVersion int           `json:"schema_version"`
	RequestID     string        `json:"request_id"`
	Schema        WireSchema    `json:"schema"`
	Blocks        []WireBlock   `json:"blocks"`
	Limits        ExtractLimits `json:"limits"`
}

// WireSchema is the schema as the extractor receives it: entity type to
// description, relation type to its ends and description.
type WireSchema struct {
	Entities  map[string]string       `json:"entities"`
	Relations map[string]WireRelation `json:"relations"`
}

// WireRelation is one relation type with its allowed end types.
type WireRelation struct {
	Head []string `json:"head"`
	Tail []string `json:"tail"`
	Desc string   `json:"desc"`
}

// WireBlock is one block of text to extract from.
type WireBlock struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// ExtractLimits may only tighten the service limits; zero means unset.
type ExtractLimits struct {
	MaxEntitiesPerBlock  int `json:"max_entities_per_block,omitempty"`
	MaxRelationsPerBlock int `json:"max_relations_per_block,omitempty"`
}

// ExtractResult is the response of POST /v1/extract. RequestID must echo
// the request (stored results keep it empty). Dropped counts what the
// service removed; it is recorded, never an error.
type ExtractResult struct {
	SchemaVersion int           `json:"schema_version"`
	RequestID     string        `json:"request_id,omitempty"`
	Model         ModelInfo     `json:"model"`
	Blocks        []BlockResult `json:"blocks"`
}

// ModelInfo names the checkpoint and device that produced a result, and
// the extractor's pipeline settings (an object kept verbatim; Go compares
// it only through ExtractorDigest).
type ModelInfo struct {
	ID       string          `json:"id"`
	Revision string          `json:"revision"`
	Device   string          `json:"device"`
	Pipeline json.RawMessage `json:"pipeline,omitempty"`
}

// ExtractorDigest is the digest of a canonical form of the extractor's
// pipeline object: keys sorted, no white space, every number parsed as a
// float64 and written with strconv.FormatFloat(f, 'g', -1, 64), so 0.05 and
// 5e-2, or 1 and 1.0, give the same digest. NaN and infinities are refused.
func ExtractorDigest(pipeline json.RawMessage) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(pipeline))
	dec.UseNumber()
	var v map[string]any
	if err := dec.Decode(&v); err != nil || v == nil {
		return "", fmt.Errorf("%w: extractor pipeline is not an object", ErrInvalidResponse)
	}
	if _, err := dec.Token(); err != io.EOF {
		return "", fmt.Errorf("%w: trailing data after extractor pipeline", ErrInvalidResponse)
	}
	var b bytes.Buffer
	if err := canonicalJSON(&b, v); err != nil {
		return "", err
	}
	sum := sha256.Sum256(b.Bytes())
	return "extractor:" + hex.EncodeToString(sum[:]), nil
}

func canonicalJSON(b *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			name, _ := json.Marshal(k)
			b.Write(name)
			b.WriteByte(':')
			if err := canonicalJSON(b, x[k]); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	case []any:
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := canonicalJSON(b, e); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case json.Number:
		f, err := strconv.ParseFloat(string(x), 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return fmt.Errorf("%w: extractor pipeline has an invalid number", ErrInvalidResponse)
		}
		b.WriteString(strconv.FormatFloat(f, 'g', -1, 64))
	default: // string, bool, nil
		out, err := json.Marshal(x)
		if err != nil {
			return err
		}
		b.Write(out)
	}
	return nil
}

// BlockResult is the extraction of one block.
type BlockResult struct {
	ID        string         `json:"id"`
	Chunks    []ChunkResult  `json:"chunks"`
	Entities  []EntitySpan   `json:"entities"`
	Relations []RelationSpan `json:"relations"`
	Dropped   *DroppedCounts `json:"dropped,omitempty"`
}

// ChunkResult is one model window over a block. Feasible false means the
// model's constraints were not satisfied, not that the text is empty.
type ChunkResult struct {
	Start    int    `json:"start"`
	End      int    `json:"end"`
	Feasible bool   `json:"feasible"`
	Status   string `json:"status"`
}

// EntitySpan is one entity span. LocalID is local to the block result.
type EntitySpan struct {
	LocalID    string  `json:"local_id"`
	Type       string  `json:"type"`
	Start      int     `json:"start"`
	End        int     `json:"end"`
	Text       string  `json:"text"`
	Confidence float64 `json:"confidence"`
}

// RelationSpan is one relation between two entity spans of the same block.
type RelationSpan struct {
	Type           string   `json:"type"`
	Head           string   `json:"head"`
	Tail           string   `json:"tail"`
	Confidence     *float64 `json:"confidence,omitempty"`
	HeadConfidence float64  `json:"head_confidence"`
	TailConfidence float64  `json:"tail_confidence"`
}

// DroppedCounts counts model output the service removed.
type DroppedCounts struct {
	InvalidSpans     int `json:"invalid_spans"`
	PartialSpans     int `json:"partial_spans"`
	InvalidRelations int `json:"invalid_relations"`
	OverLimit        int `json:"over_limit"`
}

// WireSchemaOf converts a schema to its wire form.
func WireSchemaOf(s Schema) WireSchema {
	w := WireSchema{Entities: map[string]string{}, Relations: map[string]WireRelation{}}
	for _, e := range s.Entities {
		w.Entities[e.Type] = e.Description
	}
	for _, r := range s.Relations {
		w.Relations[r.Type] = WireRelation{Head: append([]string(nil), r.Head...), Tail: append([]string(nil), r.Tail...), Desc: r.Description}
	}
	return w
}

// Request batching: a snapshot is sent in batches of at most
// MaxRequestBlocks blocks and MaxRequestRunes code points (one longer block
// goes alone). A block over MaxBlockRunes is not sent: the snapshot fails
// with too_large, matching the extractor's own block limit.
const (
	MaxRequestBlocks = 32
	MaxRequestRunes  = 16000
	MaxBlockRunes    = 20000
)

// ErrBlockTooLarge reports a block over MaxBlockRunes.
var ErrBlockTooLarge = errors.New("knowledge: block is too large")

// Requests splits the non-empty blocks of one snapshot into extraction
// requests. Request IDs derive from the snapshot and fingerprint.
func Requests(snap Snapshot, s Schema, blocks []Block) ([]ExtractRequest, error) {
	sum := sha256.Sum256([]byte(snap.Key.SourceID + "\x00" + snap.Key.Path + "\x00" + snap.Key.SHA + "\x00" + string(snap.Fingerprint)))
	base := "kg-" + hex.EncodeToString(sum[:12])
	wire := WireSchemaOf(s)
	var out []ExtractRequest
	var batch []WireBlock
	runes := 0
	flush := func() {
		if len(batch) > 0 {
			out = append(out, ExtractRequest{SchemaVersion: ExtractSchemaVersion, RequestID: base + "-" + strconv.Itoa(len(out)), Schema: wire, Blocks: batch})
			batch, runes = nil, 0
		}
	}
	for _, b := range blocks {
		n := utf8.RuneCountInString(b.Text)
		if n == 0 {
			continue
		}
		if n > MaxBlockRunes {
			return nil, ErrBlockTooLarge
		}
		if len(batch) == MaxRequestBlocks || runes+n > MaxRequestRunes {
			flush()
		}
		batch = append(batch, WireBlock{ID: b.ID, Text: b.Text})
		runes += n
	}
	flush()
	return out, nil
}

// ValidateResult checks a response against its request: the schema version,
// the echoed request ID, exactly the requested blocks, declared entity and
// relation types, spans inside the block whose text is exactly the block's
// code points [start, end), unique local IDs, relation ends naming them,
// chunk ranges and statuses, and finite scores in [0, 1].
func ValidateResult(req ExtractRequest, res ExtractResult) error {
	bad := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalidResponse, fmt.Sprintf(format, args...))
	}
	if res.SchemaVersion != ExtractSchemaVersion {
		return bad("schema version %d", res.SchemaVersion)
	}
	if req.RequestID != "" && res.RequestID != req.RequestID {
		return bad("request id mismatch")
	}
	texts := map[string][]rune{}
	for _, b := range req.Blocks {
		texts[b.ID] = []rune(b.Text)
	}
	if len(res.Blocks) != len(req.Blocks) {
		return bad("got %d blocks, want %d", len(res.Blocks), len(req.Blocks))
	}
	seen := map[string]bool{}
	for _, b := range res.Blocks {
		text, ok := texts[b.ID]
		if !ok || seen[b.ID] {
			return bad("unexpected or repeated block")
		}
		seen[b.ID] = true
		for _, c := range b.Chunks {
			if c.Start < 0 || c.End < c.Start || c.End > len(text) || (c.Status != ChunkOK && c.Status != ChunkInfeasible && c.Status != ChunkTruncatedPrevented) {
				return bad("invalid chunk in block %s", b.ID)
			}
		}
		locals := map[string]bool{}
		for _, e := range b.Entities {
			if e.LocalID == "" || locals[e.LocalID] {
				return bad("empty or repeated local id in block %s", b.ID)
			}
			locals[e.LocalID] = true
			if _, ok := req.Schema.Entities[e.Type]; !ok {
				return bad("undeclared entity type in block %s", b.ID)
			}
			if e.Start < 0 || e.End <= e.Start || e.End > len(text) || string(text[e.Start:e.End]) != e.Text {
				return bad("span does not match block text in block %s", b.ID)
			}
			if !score(e.Confidence) {
				return bad("invalid score in block %s", b.ID)
			}
		}
		for _, r := range b.Relations {
			if _, ok := req.Schema.Relations[r.Type]; !ok {
				return bad("undeclared relation type in block %s", b.ID)
			}
			if !locals[r.Head] || !locals[r.Tail] {
				return bad("relation end is not an entity of block %s", b.ID)
			}
			if !score(r.HeadConfidence) || !score(r.TailConfidence) || r.Confidence != nil && !score(*r.Confidence) {
				return bad("invalid score in block %s", b.ID)
			}
		}
	}
	return nil
}

func score(f float64) bool { return !math.IsNaN(f) && f >= 0 && f <= 1 }

// MergeResults joins the responses of one snapshot's requests into one
// result; every response must name the same model.
func MergeResults(results []ExtractResult) (ExtractResult, error) {
	out := ExtractResult{SchemaVersion: ExtractSchemaVersion, Blocks: []BlockResult{}}
	for i, r := range results {
		if i == 0 {
			out.Model = r.Model
		} else if r.Model.ID != out.Model.ID || r.Model.Revision != out.Model.Revision {
			return ExtractResult{}, fmt.Errorf("%w: model changed between requests", ErrInvalidResponse)
		}
		out.Blocks = append(out.Blocks, r.Blocks...)
	}
	return out, nil
}
