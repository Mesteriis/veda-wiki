package httpapi

import (
	"net/http"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/auth"
	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// accessKeys lists, creates, updates or revokes scoped reader keys. A new
// key's secret appears only in its creation response.
func (h handler) accessKeys(rw http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/v1/access-keys" && r.Method == "GET" {
		jsonResponse(rw, 200, h.Store.auth().ListAccessKeys())
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
			apiError(rw, domain.ErrInvalid)
			return
		}
		if err := h.Store.auth().RevokeAccessKey(input.ID); err != nil {
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
		apiError(rw, domain.ErrInvalid)
		return
	}
	expiry, err := time.Parse(time.RFC3339Nano, input.Expires)
	if err != nil {
		apiError(rw, domain.ErrInvalid)
		return
	}
	if input.ExpectedETag != nil {
		updated, err := h.Store.auth().UpdateAccessKey(input.ID, input.Sources, expiry, *input.ExpectedETag)
		if err != nil {
			apiError(rw, err)
			return
		}
		jsonResponse(rw, http.StatusOK, updated)
		return
	}
	key, err := h.Store.auth().CreateAccessKey(input.ID, input.Sources, expiry)
	if err != nil {
		apiError(rw, err)
		return
	}
	jsonResponse(rw, 201, map[string]any{"id": input.ID, "sources": input.Sources, "expires_at": expiry.UTC().Format(time.RFC3339Nano), "key": key})
}

// people lists or saves wiki users and teams.
func (h handler) people(rw http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		if r.URL.Path == "/api/v1/users" {
			jsonResponse(rw, http.StatusOK, h.Store.auth().UserViews())
		} else {
			jsonResponse(rw, http.StatusOK, h.Store.auth().TeamViews())
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
			apiError(rw, domain.ErrInvalid)
			return
		}
		team, err := h.Store.auth().SaveTeam(in.ID, in.Sources, in.Expected)
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
		apiError(rw, domain.ErrInvalid)
		return
	}
	user, err := h.Store.auth().SaveUser(in.ID, in.Teams, in.Password, in.Disabled, in.Expected)
	if err != nil {
		apiError(rw, err)
		return
	}
	status := http.StatusOK
	if in.Expected == nil {
		status = http.StatusCreated
	}
	jsonResponse(rw, status, user)
}

// This session is separate from reader and GitHub administration sessions and
// can manage only scoped reader keys, users and teams. Article and GitHub
// APIs stay unavailable.
func (h handler) browserAccessAPI(rw http.ResponseWriter, r *http.Request) {
	if h.Public || h.Store == nil || !h.adminCookieValid(r, h.AccessSessions, auth.AccessCookie) {
		http.NotFound(rw, r)
		return
	}
	if r.URL.Path == "/api/access/sources" && r.Method == "GET" {
		type sourceChoice struct {
			ID         string `json:"id"`
			Title      string `json:"title"`
			Kind       string `json:"kind"`
			Visibility string `json:"visibility"`
		}
		choices := []sourceChoice{}
		for _, source := range h.Store.sources(false) {
			choices = append(choices, sourceChoice{source.ID, source.Title, source.Kind, source.Visibility})
		}
		jsonResponse(rw, http.StatusOK, choices)
		return
	}
	if r.URL.Path != "/api/access/keys" && r.URL.Path != "/api/access/keys/revoke" && r.URL.Path != "/api/access/users" && r.URL.Path != "/api/access/teams" {
		http.NotFound(rw, r)
		return
	}
	if r.Method != "GET" && r.Method != "POST" {
		rw.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if r.Method == "POST" && !auth.SameOrigin(r) {
		jsonResponse(rw, http.StatusForbidden, map[string]string{"error": "origin rejected"})
		return
	}
	request := r.Clone(r.Context())
	request.Header = r.Header.Clone()
	request.Header.Del("Origin") // already verified above
	switch r.URL.Path {
	case "/api/access/keys":
		request.URL.Path = "/api/v1/access-keys"
	case "/api/access/keys/revoke":
		request.URL.Path = "/api/v1/access-keys/revoke"
	case "/api/access/users":
		request.URL.Path = "/api/v1/users"
	case "/api/access/teams":
		request.URL.Path = "/api/v1/teams"
	}
	h.api(rw, request, true, false)
}
