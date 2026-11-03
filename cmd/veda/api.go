package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"github.com/Mesteriis/veda-wiki/internal/github"
	"github.com/Mesteriis/veda-wiki/internal/importer"
	"github.com/Mesteriis/veda-wiki/internal/transfer"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (w Wiki) authorized(r *http.Request) bool {
	if w.Public || w.Token == "" {
		return false
	}
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		return false
	}
	provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	return subtle.ConstantTimeCompare([]byte(provided), []byte(w.Token)) == 1
}

// A reader credential grants current content only. It never grants writes or
// administrative APIs, even when presented to the private listener.
func (w Wiki) readerAuthorized(r *http.Request) bool {
	if w.Public || w.ReaderToken == "" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		return false
	}
	provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	return subtle.ConstantTimeCompare([]byte(provided), []byte(w.ReaderToken)) == 1
}
func jsonResponse(rw http.ResponseWriter, status int, v any) {
	rw.Header().Set("Content-Type", "application/json; charset=utf-8")
	rw.Header().Set("Cache-Control", "no-store")
	rw.Header().Set("X-Content-Type-Options", "nosniff")
	rw.WriteHeader(status)
	json.NewEncoder(rw).Encode(v)
}
func decodeBody(rw http.ResponseWriter, r *http.Request, dst any) error {
	return decodeLimited(rw, r, dst, 300000)
}
func decodeLimited(rw http.ResponseWriter, r *http.Request, dst any, limit int64) error {
	decoder := json.NewDecoder(http.MaxBytesReader(rw, r.Body, limit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("trailing input")
	}
	return nil
}
func apiError(rw http.ResponseWriter, err error) {
	if errors.Is(err, errLocalImportActive) {
		jsonResponse(rw, http.StatusConflict, map[string]string{"error": "local_import_active"})
		return
	}
	if errors.Is(err, importer.ErrAssetUnavailable) {
		jsonResponse(rw, http.StatusServiceUnavailable, map[string]string{"error": "asset_unavailable"})
		return
	}
	var upstream *github.RemoteError
	if errors.As(err, &upstream) {
		code := 502
		if upstream.Code == "repository_unavailable" {
			code = 404
		}
		if upstream.Code == "permission_denied" {
			code = 403
		}
		if upstream.Code == "rate_limited" || upstream.Code == "upstream_unavailable" {
			code = 503
		}
		jsonResponse(rw, code, map[string]string{"error": upstream.Code})
		return
	}
	code := 500
	message := "storage operation failed"
	if errors.Is(err, ErrInvalid) {
		code = 400
		message = "invalid request"
	}
	if errors.Is(err, ErrConflict) {
		code = 409
		message = "revision conflict"
	}
	if errors.Is(err, ErrNotFound) {
		code = 404
		message = "not found"
	}
	jsonResponse(rw, code, map[string]string{"error": message})
}
func (w Wiki) api(rw http.ResponseWriter, r *http.Request, owner, reader bool) {
	validation := r.Method == "POST" && (r.URL.Path == "/api/v1/articles/translation-validate" || r.URL.Path == "/api/v1/articles/transfer-plan") && reader
	if r.Method != "GET" && ((!owner && !validation) || r.Header.Get("Origin") != "") {
		jsonResponse(rw, 403, map[string]string{"error": "owner authorization required; browser writes are disabled"})
		return
	}
	w.routeAPI(rw, r, owner, reader)
}

// routeAPI checks the required role in every route, independently of the
// shared guard in api(). Non-owners may only read and run the two validations.
func (w Wiki) routeAPI(rw http.ResponseWriter, r *http.Request, owner, reader bool) {
	if r.Method != "GET" && !owner && !(r.Method == "POST" && reader && (r.URL.Path == "/api/v1/articles/translation-validate" || r.URL.Path == "/api/v1/articles/transfer-plan")) {
		jsonResponse(rw, 403, map[string]string{"error": "owner authorization required"})
		return
	}
	if r.URL.Path == "/api/v1/access-keys" || r.URL.Path == "/api/v1/access-keys/revoke" {
		if !owner {
			http.NotFound(rw, r)
			return
		}
		if r.URL.Path == "/api/v1/access-keys" && r.Method == "GET" {
			jsonResponse(rw, 200, w.Store.auth().ListAccessKeys())
			return
		}
		if r.Method != "POST" {
			rw.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path == "/api/v1/access-keys/revoke" {
			var input struct {
				ID string `json:"id"`
			}
			if decodeBody(rw, r, &input) != nil {
				apiError(rw, ErrInvalid)
				return
			}
			if err := w.Store.auth().RevokeAccessKey(input.ID); err != nil {
				apiError(rw, err)
				return
			}
			jsonResponse(rw, 200, map[string]string{"status": "revoked"})
			return
		}
		var input struct {
			ID           string   `json:"id"`
			Sources      []string `json:"sources"`
			Expires      string   `json:"expires_at"`
			ExpectedETag *string  `json:"expected_etag"`
		}
		if decodeBody(rw, r, &input) != nil {
			apiError(rw, ErrInvalid)
			return
		}
		expiry, err := time.Parse(time.RFC3339Nano, input.Expires)
		if err != nil {
			apiError(rw, ErrInvalid)
			return
		}
		if input.ExpectedETag != nil {
			updated, err := w.Store.auth().UpdateAccessKey(input.ID, input.Sources, expiry, *input.ExpectedETag)
			if err != nil {
				apiError(rw, err)
				return
			}
			jsonResponse(rw, http.StatusOK, updated)
			return
		}
		key, err := w.Store.auth().CreateAccessKey(input.ID, input.Sources, expiry)
		if err != nil {
			apiError(rw, err)
			return
		}
		jsonResponse(rw, 201, map[string]any{"id": input.ID, "sources": input.Sources, "expires_at": expiry.UTC().Format(time.RFC3339Nano), "key": key})
		return
	}
	if r.URL.Path == "/api/v1/users" || r.URL.Path == "/api/v1/teams" {
		if !owner {
			http.NotFound(rw, r)
			return
		}
		if r.Method == "GET" {
			if r.URL.Path == "/api/v1/users" {
				jsonResponse(rw, http.StatusOK, w.Store.auth().UserViews())
			} else {
				jsonResponse(rw, http.StatusOK, w.Store.auth().TeamViews())
			}
			return
		}
		if r.Method != "POST" {
			rw.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path == "/api/v1/teams" {
			var in struct {
				ID       string   `json:"id"`
				Sources  []string `json:"sources"`
				Expected *int     `json:"expected_revision"`
			}
			if decodeBody(rw, r, &in) != nil {
				apiError(rw, ErrInvalid)
				return
			}
			team, err := w.Store.auth().SaveTeam(in.ID, in.Sources, in.Expected)
			if err != nil {
				apiError(rw, err)
				return
			}
			status := http.StatusOK
			if in.Expected == nil {
				status = http.StatusCreated
			}
			jsonResponse(rw, status, team)
			return
		}
		var in struct {
			ID       string    `json:"id"`
			Teams    *[]string `json:"teams"`
			Password *string   `json:"password"`
			Disabled *bool     `json:"disabled"`
			Expected *int      `json:"expected_revision"`
		}
		if decodeBody(rw, r, &in) != nil {
			apiError(rw, ErrInvalid)
			return
		}
		user, err := w.Store.auth().SaveUser(in.ID, in.Teams, in.Password, in.Disabled, in.Expected)
		if err != nil {
			apiError(rw, err)
			return
		}
		status := http.StatusOK
		if in.Expected == nil {
			status = http.StatusCreated
		}
		jsonResponse(rw, status, user)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/v1/github/") {
		w.githubAPI(rw, r, owner)
		return
	}
	switch r.URL.Path {
	case "/api/v1/articles/new-proposal-base":
		if !owner && !reader {
			http.NotFound(rw, r)
			return
		}
		if r.Method != "GET" {
			rw.WriteHeader(405)
			return
		}
		sourceID, p := r.URL.Query().Get("source_id"), r.URL.Query().Get("path")
		if !identifier.MatchString(sourceID) || !safePath(p) {
			apiError(rw, ErrInvalid)
			return
		}
		result, err := w.Store.newProposalBase(sourceID, p)
		if err != nil {
			apiError(rw, err)
			return
		}
		jsonResponse(rw, 200, result)
	case "/api/v1/articles/proposal-base":
		if !owner && !reader {
			http.NotFound(rw, r)
			return
		}
		if r.Method != "GET" {
			rw.WriteHeader(405)
			return
		}
		sourceID, p := r.URL.Query().Get("source_id"), r.URL.Query().Get("path")
		if !identifier.MatchString(sourceID) || !safePath(p) {
			apiError(rw, ErrInvalid)
			return
		}
		result, err := w.Store.proposalBase(sourceID, p)
		if err != nil {
			apiError(rw, err)
			return
		}
		jsonResponse(rw, 200, result)
	case "/api/v1/articles/display":
		if r.Method != "GET" {
			rw.WriteHeader(405)
			return
		}
		sourceID, p := r.URL.Query().Get("source_id"), r.URL.Query().Get("path")
		if !identifier.MatchString(sourceID) || !safePath(p) {
			apiError(rw, ErrInvalid)
			return
		}
		page, err := w.Store.page(sourceID+"/"+p, !owner && !reader)
		if err != nil {
			apiError(rw, err)
			return
		}
		jsonResponse(rw, 200, map[string]any{
			"source_id": sourceID, "path": p, "title": page.Title,
			"text": page.Text, "sha256": page.SHA, "original_sha256": page.OriginalSHA,
			"translated": page.Translated, "translation_state": page.TranslationState,
			"translation_origin": page.TranslationOrigin, "program_documentation": page.ProgramDocumentation,
		})
	case "/api/v1/articles/excerpt", "/api/v1/articles/display-excerpt":
		if r.Method != "GET" {
			rw.WriteHeader(405)
			return
		}
		q := r.URL.Query()
		sourceID, p, expected := q.Get("source_id"), q.Get("path"), q.Get("expected_sha256")
		display := r.URL.Path == "/api/v1/articles/display-excerpt"
		expectedOriginal := q.Get("expected_original_sha256")
		if !identifier.MatchString(sourceID) || p == "" || (expected != "" && len(expected) != 64) {
			apiError(rw, ErrInvalid)
			return
		}
		if expected != "" {
			if _, err := hexKey(expected); err != nil {
				apiError(rw, ErrInvalid)
				return
			}
		}
		if display && expectedOriginal != "" {
			if _, err := hexKey(expectedOriginal); err != nil {
				apiError(rw, ErrInvalid)
				return
			}
		}
		offset, limit := 0, 8000
		if q.Has("offset") {
			n, err := strconv.Atoi(q.Get("offset"))
			if err != nil || n < 0 {
				apiError(rw, ErrInvalid)
				return
			}
			offset = n
		}
		if q.Has("limit") {
			n, err := strconv.Atoi(q.Get("limit"))
			if err != nil || n < 1 || n > 16000 {
				apiError(rw, ErrInvalid)
				return
			}
			limit = n
		}
		if offset > 0 && (expected == "" || (display && expectedOriginal == "")) {
			apiError(rw, ErrInvalid)
			return
		}
		if display {
			page, err := w.Store.page(sourceID+"/"+p, !owner && !reader)
			if err != nil {
				apiError(rw, err)
				return
			}
			if (expected != "" && page.SHA != expected) || (expectedOriginal != "" && page.OriginalSHA != expectedOriginal) {
				apiError(rw, ErrConflict)
				return
			}
			text := []rune(page.Text)
			if offset > len(text) {
				apiError(rw, ErrInvalid)
				return
			}
			end := offset + min(limit, len(text)-offset)
			var next *int
			if end < len(text) {
				next = &end
			}
			jsonResponse(rw, 200, map[string]any{"source_id": sourceID, "path": p, "sha256": page.SHA, "original_sha256": page.OriginalSHA, "translated": page.Translated, "translation_state": page.TranslationState, "translation_origin": page.TranslationOrigin, "program_documentation": page.ProgramDocumentation, "offset": offset, "next_offset": next, "total_characters": len(text), "text": string(text[offset:end])})
			return
		}
		a, err := w.Store.article(sourceID, p, !owner && !reader)
		if err != nil {
			apiError(rw, err)
			return
		}
		revision := a.Versions[len(a.Versions)-1]
		if expected != "" && revision.SHA != expected {
			apiError(rw, ErrConflict)
			return
		}
		text := []rune(revision.Text)
		if offset > len(text) {
			apiError(rw, ErrInvalid)
			return
		}
		end := offset + min(limit, len(text)-offset)
		var next *int
		if end < len(text) {
			next = &end
		}
		jsonResponse(rw, 200, map[string]any{"source_id": sourceID, "path": p, "revision": revision.Number, "sha256": revision.SHA, "offset": offset, "next_offset": next, "total_characters": len(text), "text": string(text[offset:end])})
	case "/api/v1/context":
		if r.Method != "GET" {
			rw.WriteHeader(405)
			return
		}
		sourceID, target := r.URL.Query().Get("source_id"), r.URL.Query().Get("path")
		if !identifier.MatchString(sourceID) {
			apiError(rw, ErrInvalid)
			return
		}
		result, err := w.Store.agentContext(sourceID, target, !owner && !reader)
		if errors.Is(err, errContextLimit) {
			jsonResponse(rw, 413, map[string]string{"error": "context exceeds response limit"})
			return
		}
		if err != nil {
			apiError(rw, err)
			return
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			apiError(rw, err)
			return
		}
		if len(encoded) >= 2000000 {
			jsonResponse(rw, 413, map[string]string{"error": "context exceeds response limit"})
			return
		}
		jsonResponse(rw, 200, result)
	case "/api/v1/articles/translation-validate":
		if r.Method != "POST" {
			rw.WriteHeader(405)
			return
		}
		var in translationRequest
		if decodeLimited(rw, r, &in, 7<<20) != nil {
			apiError(rw, ErrInvalid)
			return
		}
		result, err := w.Store.translation().Validate(in)
		if err != nil {
			apiError(rw, err)
			return
		}
		jsonResponse(rw, 200, result)
	case "/api/v1/search":
		if r.Method != "GET" {
			rw.WriteHeader(405)
			return
		}
		query := strings.TrimSpace(r.URL.Query().Get("q"))
		sourceID := r.URL.Query().Get("source_id")
		if query == "" || len(query) > 300 || (sourceID != "" && !identifier.MatchString(sourceID)) {
			apiError(rw, ErrInvalid)
			return
		}
		limit, offset := 20, 0
		if r.URL.Query().Has("limit") {
			n, err := strconv.Atoi(r.URL.Query().Get("limit"))
			if err != nil || n < 1 || n > 100 {
				apiError(rw, ErrInvalid)
				return
			}
			limit = n
		}
		if r.URL.Query().Has("offset") {
			n, err := strconv.Atoi(r.URL.Query().Get("offset"))
			if err != nil || n < 0 {
				apiError(rw, ErrInvalid)
				return
			}
			offset = n
		}
		all := w.Store.findArticles(query, sourceID, !owner && !reader)
		start := min(offset, len(all))
		end := start + min(limit, len(all)-start)
		jsonResponse(rw, 200, map[string]any{"results": all[start:end], "total": len(all), "offset": offset})
	case "/api/v1/moves/cancel":
		if !owner {
			http.NotFound(rw, r)
			return
		}
		if r.Method != "POST" {
			rw.WriteHeader(405)
			return
		}
		var in struct {
			ID      string `json:"id"`
			Confirm bool   `json:"confirm_cancel"`
		}
		if decodeBody(rw, r, &in) != nil {
			apiError(rw, ErrInvalid)
			return
		}
		if err := w.Store.transfer().CancelMove(in.ID, in.Confirm); err != nil {
			apiError(rw, err)
			return
		}
		jsonResponse(rw, 200, map[string]string{"state": "cancelled"})
	case "/api/v1/moves":
		if !owner {
			http.NotFound(rw, r)
			return
		}
		if r.Method == "GET" {
			all := w.Store.transfer().Moves()
			limit, offset := 30, 0
			if r.URL.Query().Has("limit") {
				n, e := strconv.Atoi(r.URL.Query().Get("limit"))
				if e != nil || n < 1 || n > 100 {
					apiError(rw, ErrInvalid)
					return
				}
				limit = n
			}
			if r.URL.Query().Has("offset") {
				n, e := strconv.Atoi(r.URL.Query().Get("offset"))
				if e != nil || n < 0 {
					apiError(rw, ErrInvalid)
					return
				}
				offset = n
			}
			start := min(offset, len(all))
			end := min(start+limit, len(all))
			jsonResponse(rw, 200, map[string]any{"moves": all[start:end], "total": len(all), "offset": offset})
			return
		}
		if r.Method != "POST" {
			rw.WriteHeader(405)
			return
		}
		var in transfer.MoveRequest
		if decodeBody(rw, r, &in) != nil {
			apiError(rw, ErrInvalid)
			return
		}
		result, e := w.Store.transfer().Move(in)
		if e != nil {
			apiError(rw, e)
			return
		}
		if w.Sync != nil {
			w.Sync.Wake()
		}
		jsonResponse(rw, 201, result)
	case "/api/v1/articles/translation-needed":
		if !owner && !reader {
			http.NotFound(rw, r)
			return
		}
		if r.Method != "GET" {
			rw.WriteHeader(405)
			return
		}
		sourceID := r.URL.Query().Get("source_id")
		if sourceID != "" && !identifier.MatchString(sourceID) {
			apiError(rw, ErrInvalid)
			return
		}
		limit, offset := 30, 0
		if r.URL.Query().Has("limit") {
			n, e := strconv.Atoi(r.URL.Query().Get("limit"))
			if e != nil || n < 1 || n > 100 {
				apiError(rw, ErrInvalid)
				return
			}
			limit = n
		}
		if r.URL.Query().Has("offset") {
			n, e := strconv.Atoi(r.URL.Query().Get("offset"))
			if e != nil || n < 0 {
				apiError(rw, ErrInvalid)
				return
			}
			offset = n
		}
		all := w.Store.translation().Candidates()
		if sourceID != "" {
			filtered := make([]translationCandidate, 0)
			for _, candidate := range all {
				if candidate.SourceID == sourceID {
					filtered = append(filtered, candidate)
				}
			}
			all = filtered
		}
		start := min(offset, len(all))
		end := min(start+limit, len(all))
		jsonResponse(rw, 200, map[string]any{"candidates": all[start:end], "total": len(all), "offset": offset})
	case "/api/v1/articles/translation":
		if r.Method != "POST" {
			rw.WriteHeader(405)
			return
		}
		var in translationRequest
		if decodeLimited(rw, r, &in, 7<<20) != nil {
			apiError(rw, ErrInvalid)
			return
		}
		tr, e := w.Store.translation().Submit(in)
		if e != nil {
			apiError(rw, e)
			return
		}
		jsonResponse(rw, 200, tr)
	case "/api/v1/articles/transfer-plan":
		if r.Method != "POST" {
			rw.WriteHeader(405)
			return
		}
		var in transfer.CopyRequest
		if decodeBody(rw, r, &in) != nil {
			apiError(rw, ErrInvalid)
			return
		}
		plan, err := w.Store.transfer().PlanTransfer(in)
		if err != nil {
			apiError(rw, err)
			return
		}
		jsonResponse(rw, 200, plan)
	case "/api/v1/sources/mirrors":
		if !owner {
			http.NotFound(rw, r)
			return
		}
		if r.Method == "GET" {
			jsonResponse(rw, 200, w.Store.mirrors())
			return
		}
		if r.Method != "POST" {
			rw.WriteHeader(405)
			return
		}
		var in mirrorRequest
		if decodeBody(rw, r, &in) != nil {
			apiError(rw, ErrInvalid)
			return
		}
		if in.Enabled != nil && *in.Enabled && w.Sync == nil {
			jsonResponse(rw, http.StatusServiceUnavailable, map[string]string{"error": "sync_unavailable"})
			return
		}
		_ = http.NewResponseController(rw).SetWriteDeadline(time.Now().Add(2 * time.Minute))
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		mirror, err := w.Store.configureMirror(ctx, in)
		if err != nil {
			apiError(rw, err)
			return
		}
		if w.Sync != nil && mirror.Enabled {
			w.Sync.Wake()
		}
		jsonResponse(rw, 200, mirror)
	case "/api/v1/sources/reconcile":
		if r.Method != "POST" {
			rw.WriteHeader(405)
			return
		}
		var in reconcileRequest
		if decodeBody(rw, r, &in) != nil {
			apiError(rw, ErrInvalid)
			return
		}
		_ = http.NewResponseController(rw).SetWriteDeadline(time.Now().Add(2 * time.Minute))
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		result, err := w.Store.reconcileSources(ctx, in)
		if err != nil {
			apiError(rw, err)
			return
		}
		if w.Sync != nil {
			w.Sync.Wake()
		}
		jsonResponse(rw, 200, result)
	case "/api/v1/articles/copy":
		if r.Method != "POST" {
			rw.WriteHeader(405)
			return
		}
		var in transfer.CopyRequest
		if err := decodeBody(rw, r, &in); err != nil {
			apiError(rw, ErrInvalid)
			return
		}
		rev, err := w.Store.transfer().Copy(in)
		if err != nil {
			apiError(rw, err)
			return
		}
		if w.Sync != nil {
			w.Sync.Wake()
		}
		jsonResponse(rw, 201, rev)
	case "/api/v1/conflicts":
		if !owner {
			http.NotFound(rw, r)
			return
		}
		if r.Method != "GET" {
			rw.WriteHeader(405)
			return
		}
		all := w.Store.importer().Conflicts()
		query := r.URL.Query()
		if query.Get("source_id") != "" {
			index, e := strconv.Atoi(query.Get("index"))
			if e != nil || index < 0 {
				apiError(rw, ErrInvalid)
				return
			}
			for _, record := range all {
				if record.SourceID == query.Get("source_id") && record.Path == query.Get("path") && record.Index == index {
					jsonResponse(rw, 200, map[string]any{"conflicts": []importer.ConflictRecord{record}, "total": 1, "offset": 0})
					return
				}
			}
			apiError(rw, ErrNotFound)
			return
		}
		limit, offset := 30, 0
		if query.Has("limit") {
			value, e := strconv.Atoi(query.Get("limit"))
			if e != nil || value < 1 || value > 100 {
				apiError(rw, ErrInvalid)
				return
			}
			limit = value
		}
		if query.Has("offset") {
			value, e := strconv.Atoi(query.Get("offset"))
			if e != nil || value < 0 {
				apiError(rw, ErrInvalid)
				return
			}
			offset = value
		}
		start := offset
		if start > len(all) {
			start = len(all)
		}
		end := start + limit
		if end > len(all) {
			end = len(all)
		}
		rows := all[start:end]
		for i := range rows {
			rows[i].Conflict.Incoming = ""
		}
		jsonResponse(rw, 200, map[string]any{"conflicts": rows, "total": len(all), "offset": offset})
	case "/api/v1/conflicts/resolve":
		if !owner {
			http.NotFound(rw, r)
			return
		}
		if r.Method != "POST" {
			rw.WriteHeader(405)
			return
		}
		var in struct {
			SourceID   string `json:"source_id"`
			Path       string `json:"path"`
			Index      *int   `json:"index"`
			Expected   int    `json:"expected_revision"`
			Resolution string `json:"resolution"`
		}
		if e := decodeBody(rw, r, &in); e != nil || in.Index == nil {
			apiError(rw, ErrInvalid)
			return
		}
		if e := w.Store.importer().ResolveConflict(in.SourceID, in.Path, *in.Index, in.Expected, in.Resolution); e != nil {
			apiError(rw, e)
			return
		}
		if w.Sync != nil {
			w.Store.retryConnection(in.SourceID)
			w.Sync.Wake()
		}
		jsonResponse(rw, 200, map[string]string{"status": "resolved"})
	case "/api/v1/import":
		if !owner {
			http.NotFound(rw, r)
			return
		}
		if r.Method != "POST" {
			rw.WriteHeader(405)
			return
		}
		var batch importer.Batch
		if e := importer.Decode(rw, r, &batch); e != nil {
			apiError(rw, ErrInvalid)
			return
		}
		result, e := w.Store.importer().Import(batch)
		if e != nil {
			apiError(rw, e)
			return
		}
		jsonResponse(rw, 200, result)
	case "/api/v1/sources":
		switch r.Method {
		case "GET":
			jsonResponse(rw, 200, w.Store.sources(!owner && !reader))
		case "POST":
			var src Source
			if err := decodeBody(rw, r, &src); err != nil {
				apiError(rw, ErrInvalid)
				return
			}
			if err := w.Store.addSource(src); err != nil {
				apiError(rw, err)
				return
			}
			jsonResponse(rw, 201, src)
		default:
			rw.WriteHeader(405)
		}
	case "/api/v1/articles":
		switch r.Method {
		case "GET":
			sourceID, p := r.URL.Query().Get("source_id"), r.URL.Query().Get("path")
			if sourceID == "" && p == "" {
				jsonResponse(rw, 200, w.Store.snapshot(!owner && !reader).Pages)
				return
			}
			a, err := w.Store.article(sourceID, p, !owner && !reader)
			if err != nil {
				apiError(rw, err)
				return
			}
			if r.URL.Query().Get("history") != "1" || !owner {
				a.Versions = a.Versions[len(a.Versions)-1:]
				a.Conflicts = nil
			}
			if reader && !owner {
				a.Translation = nil
				a.CopiedFrom = nil
				a.ReconcileBase = nil
				a.Published = nil
				a.PublishedLocal = false
				a.UpstreamHash = ""
			}
			jsonResponse(rw, 200, a)
		case "POST", "PUT":
			var input struct {
				SourceID string `json:"source_id"`
				Path     string `json:"path"`
				Text     string `json:"text"`
				Expected *int   `json:"expected_revision"`
			}
			if err := decodeLimited(rw, r, &input, 7<<20); err != nil {
				apiError(rw, ErrInvalid)
				return
			}
			if input.Expected == nil || (r.Method == "POST" && *input.Expected != 0) || (r.Method == "PUT" && *input.Expected < 1) {
				apiError(rw, ErrInvalid)
				return
			}
			rev, err := w.Store.put(input.SourceID, input.Path, input.Text, *input.Expected)
			if err != nil {
				apiError(rw, err)
				return
			}
			if w.Sync != nil {
				w.Sync.Wake()
			}
			status := 200
			if r.Method == "POST" {
				status = 201
			}
			jsonResponse(rw, status, rev)
		default:
			rw.WriteHeader(405)
		}
	default:
		http.NotFound(rw, r)
	}
}
