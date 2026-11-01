package main

import "net/http"

// This session is separate from reader and GitHub administration sessions and
// can manage only scoped reader keys. Article and GitHub APIs stay unavailable.
func (w Wiki) browserAccessAPI(rw http.ResponseWriter, r *http.Request) {
	if w.Public || w.Store == nil || !w.AccessSessions.validCookie(r, accessSessionCookie) {
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
		for _, source := range w.Store.sources(false) {
			choices = append(choices, sourceChoice{source.ID, source.Title, source.Kind, source.Visibility})
		}
		jsonResponse(rw, http.StatusOK, choices)
		return
	}
	if r.URL.Path != "/api/access/keys" && r.URL.Path != "/api/access/keys/revoke" {
		http.NotFound(rw, r)
		return
	}
	if r.Method != "GET" && r.Method != "POST" {
		rw.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if r.Method == "POST" && !sameOrigin(r) {
		jsonResponse(rw, http.StatusForbidden, map[string]string{"error": "origin rejected"})
		return
	}
	request := r.Clone(r.Context())
	request.Header = r.Header.Clone()
	request.Header.Del("Origin") // already verified above
	request.URL.Path = "/api/v1/access-keys"
	if r.URL.Path == "/api/access/keys/revoke" {
		request.URL.Path += "/revoke"
	}
	w.api(rw, request, true, false)
}
