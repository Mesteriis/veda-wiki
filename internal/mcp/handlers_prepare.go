package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/Mesteriis/veda-wiki/internal/mcp/client"
)

// prepareHandlers build non-persistent proposals. They only read, so reader
// keys may use them.
var prepareHandlers = map[string]handler{
	"veda_prepare_update":      prepareUpdate,
	"veda_prepare_new_article": prepareNewArticle,
	"veda_prepare_translation": prepareTranslation,
}

const newArticleNote = "Proposal only; deleted history or concurrent writes may cause a conflict. " +
	"The path is not reserved and an authorized write is still required."

const translationNote = "Proposal only; validation cannot establish translation quality. " +
	"Review and an authorized translation submission are still required."

func proposalSHA(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// fields returns the named values of o, failing on the first missing one.
func fields(o object, names ...string) (map[string]any, error) {
	out := make(map[string]any, len(names))
	for _, name := range names {
		value, err := o.get(name)
		if err != nil {
			return nil, err
		}
		out[name] = value
	}
	return out, nil
}

func getObject(ctx context.Context, c *client.Client, route string, params url.Values) (object, error) {
	raw, err := c.Do(ctx, http.MethodGet, route+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	return decodeObject(raw)
}

func prepareUpdate(ctx context.Context, c *client.Client, args arguments) (any, error) {
	sourceID, path, proposed := args.str("source_id"), args.str("path"), args.str("text")
	current, err := getObject(ctx, c, "/api/v1/articles/proposal-base", url.Values{"source_id": {sourceID}, "path": {path}})
	if err != nil {
		return nil, err
	}
	revision, err := current.get("revision")
	if err != nil {
		return nil, err
	}
	if len(proposed) > MaxArticleBytes || !equalsInt(revision, args.integer("expected_revision")) {
		return nil, errInvalid // invalid or stale proposal
	}
	f, err := fields(current, "sha256", "unresolved_conflict", "conflict_resolution_blocked_by_write",
		"write_blocked_by_move", "write_blocked_by_bundle", "source_status")
	if err != nil {
		return nil, err
	}
	return object{
		{"source_id", sourceID}, {"path", path}, {"expected_revision", revision}, {"base_sha256", f["sha256"]},
		{"proposed_sha256", proposalSHA(proposed)}, {"text", proposed}, {"saved", false},
		{"unresolved_conflict", f["unresolved_conflict"]},
		{"conflict_resolution_blocked_by_write", f["conflict_resolution_blocked_by_write"]},
		{"write_blocked_by_move", f["write_blocked_by_move"]}, {"write_blocked_by_bundle", f["write_blocked_by_bundle"]},
		{"source_status", f["source_status"]}, {"note", updateNote(f)},
	}, nil
}

func updateNote(f map[string]any) string {
	switch {
	case truthy(f["write_blocked_by_move"]):
		return "Proposal only; a move blocks writes. Investigate the move and any conflict before an authorized write."
	case truthy(f["write_blocked_by_bundle"]):
		return "Proposal only; a linked bundle blocks writes. Investigate the bundle and any conflict before an authorized write."
	case truthy(f["conflict_resolution_blocked_by_write"]) && truthy(f["unresolved_conflict"]):
		return "Proposal only; a prepared write or active bundle blocks conflict resolution and needs investigation."
	case truthy(f["unresolved_conflict"]):
		return "Proposal only; resolve the import conflict before an authorized write."
	}
	return "Proposal only; review and an authorized write are still required. Conflict status may change before writing."
}

func prepareNewArticle(ctx context.Context, c *client.Client, args arguments) (any, error) {
	sourceID, path, proposed := args.str("source_id"), args.str("path"), args.str("text")
	if len(proposed) > MaxArticleBytes {
		return nil, errInvalid
	}
	current, err := getObject(ctx, c, "/api/v1/articles/new-proposal-base", url.Values{"source_id": {sourceID}, "path": {path}})
	if err != nil {
		return nil, err
	}
	status, err := current.get("source_status")
	if err != nil {
		return nil, err
	}
	return object{
		{"source_id", sourceID}, {"path", path}, {"expected_revision", 0}, {"proposed_sha256", proposalSHA(proposed)},
		{"text", proposed}, {"saved", false}, {"source_status", status}, {"note", newArticleNote},
	}, nil
}

// isProgramSource reports whether the first listed source with this id has
// kind "program". A listed source without an id before the match is invalid.
func isProgramSource(raw json.RawMessage, sourceID string) (bool, error) {
	sources, err := decodeArray(raw)
	if err != nil {
		return false, err
	}
	for _, item := range sources {
		source, err := decodeObject(item)
		if err != nil {
			return false, err
		}
		id, err := source.get("id")
		if err != nil {
			return false, err
		}
		if pyValue(id) == sourceID {
			kind, _ := source.get("kind")
			return pyValue(kind) == "program", nil
		}
	}
	return false, nil
}

func prepareTranslation(ctx context.Context, c *client.Client, args arguments) (any, error) {
	sourceID, path, proposed := args.str("source_id"), args.str("path"), args.str("text")
	sources, err := c.Do(ctx, http.MethodGet, "/api/v1/sources", nil)
	if err != nil {
		return nil, err
	}
	program, err := isProgramSource(sources, sourceID)
	if err != nil {
		return nil, err
	}
	if !program || !strings.HasPrefix(path, "docs/") {
		return nil, errInvalid // program documentation required
	}
	current, err := readLatest(ctx, c, sourceID, path)
	if err != nil {
		return nil, err
	}
	f, err := fields(current, "revision", "sha256")
	if err != nil {
		return nil, err
	}
	if proposed == "" || len(proposed) > MaxArticleBytes || !equalsInt(f["revision"], args.integer("expected_revision")) {
		return nil, errInvalid // invalid or stale translation proposal
	}
	validationRaw, err := c.Do(ctx, http.MethodPost, "/api/v1/articles/translation-validate", object{
		{"source_id", sourceID}, {"path", path}, {"expected_revision", f["revision"]},
		{"original_sha256", f["sha256"]}, {"text", proposed},
	})
	if err != nil {
		return nil, err
	}
	validation, err := decodeObject(validationRaw)
	if err != nil {
		return nil, err
	}
	v, err := fields(validation, "translation_fingerprint", "source_status")
	if err != nil {
		return nil, err
	}
	return object{
		{"source_id", sourceID}, {"path", path}, {"expected_revision", f["revision"]}, {"original_sha256", f["sha256"]},
		{"text", proposed}, {"expected_translation_fingerprint", v["translation_fingerprint"]}, {"validation", validationRaw},
		{"source_status", v["source_status"]}, {"saved", false}, {"note", translationNote},
	}, nil
}
