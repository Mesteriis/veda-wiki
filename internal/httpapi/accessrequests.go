package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/auth"
	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/notify"
)

// defaultRequestKeyDays is the validity of a key issued for a request when
// the owner gives no expiry.
const defaultRequestKeyDays = 90

// clientAddress is the visitor's address: X-Real-IP only when the
// connection comes from a configured trusted proxy, otherwise the peer.
func (h handler) clientAddress(r *http.Request) string {
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		peer = r.RemoteAddr
	}
	peerIP := net.ParseIP(peer)
	for _, proxy := range h.TrustedProxies {
		if peerIP != nil && peerIP.Equal(net.ParseIP(proxy)) {
			if real := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); real != nil {
				return real.String()
			}
		}
	}
	return peer
}

// publicAccessRequest takes a visitor's request for API/MCP access to public
// sources. It is the only write an anonymous visitor can make, on any
// listener: same origin, a small body, rate limited, public sources only.
func (h handler) publicAccessRequest(rw http.ResponseWriter, r *http.Request) {
	if h.Notify == nil || h.Store == nil {
		http.NotFound(rw, r)
		return
	}
	if r.Method != "POST" {
		rw.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !auth.SameOrigin(r) {
		jsonResponse(rw, http.StatusForbidden, map[string]string{"error": "origin rejected"})
		return
	}
	var in struct {
		Sources  []string `json:"sources"`
		Telegram string   `json:"telegram"`
		Comment  string   `json:"comment"`
		Website  string   `json:"website"`
	}
	if decodeLimited(rw, r, &in, 4096) != nil {
		apiError(rw, domain.ErrInvalid)
		return
	}
	accepted := map[string]bool{"status": true}
	if in.Website != "" {
		// The hidden field is filled only by bots: answer as if accepted.
		jsonResponse(rw, http.StatusAccepted, accepted)
		return
	}
	comment, ok := notify.CleanComment(in.Comment)
	public := map[string]string{}
	for _, source := range h.Store.sources(true) {
		public[source.ID] = source.Title
	}
	seen := map[string]bool{}
	var titles []string
	for _, id := range in.Sources {
		if public[id] == "" || seen[id] {
			ok = false
			break
		}
		seen[id] = true
		titles = append(titles, public[id])
	}
	if !ok || len(in.Sources) == 0 || len(in.Sources) > notify.MaxSources || !notify.ValidNick(in.Telegram) {
		apiError(rw, domain.ErrInvalid)
		return
	}
	if !h.Notify.Limiter.Allow(h.clientAddress(r), time.Now()) {
		h.Notify.FormLimited(context.WithoutCancel(r.Context()), time.Now())
		jsonResponse(rw, http.StatusTooManyRequests, map[string]string{"error": "try again later"})
		return
	}
	request, err := h.Notify.Store.AddRequest(r.Context(), notify.Request{ID: randomID(), Sources: in.Sources, Telegram: in.Telegram, Comment: comment}, time.Now())
	if errors.Is(err, notify.ErrQueueFull) {
		h.Notify.FormLimited(context.WithoutCancel(r.Context()), time.Now())
		jsonResponse(rw, http.StatusTooManyRequests, map[string]string{"error": "try again later"})
		return
	}
	if err != nil {
		apiError(rw, err)
		return
	}
	h.Notify.RequestCreated(context.WithoutCancel(r.Context()), request, titles)
	jsonResponse(rw, http.StatusAccepted, accepted)
}

func randomID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// accessRequestsAPI serves the access-request and Telegram tabs of the
// settings to an access administrator; the caller checked the session and
// the origin of writes.
func (h handler) accessRequestsAPI(rw http.ResponseWriter, r *http.Request) {
	if h.Notify == nil {
		jsonResponse(rw, http.StatusServiceUnavailable, map[string]string{"error": "notifications_unavailable"})
		return
	}
	ctx := r.Context()
	switch {
	case r.URL.Path == "/api/access/requests" && r.Method == "GET":
		list, err := h.Notify.Store.Requests(ctx)
		if err != nil {
			apiError(rw, err)
			return
		}
		jsonResponse(rw, 200, list)
	case r.URL.Path == "/api/access/requests/approve" && r.Method == "POST":
		h.approveRequest(rw, r)
	case r.URL.Path == "/api/access/requests/reject" && r.Method == "POST":
		var in struct {
			ID string `json:"id"`
		}
		if decodeLimited(rw, r, &in, 1024) != nil {
			apiError(rw, domain.ErrInvalid)
			return
		}
		if err := h.Notify.Store.Decide(ctx, in.ID, "rejected", "", time.Now()); err != nil {
			apiError(rw, err)
			return
		}
		jsonResponse(rw, 200, map[string]string{"state": "rejected"})
	case r.URL.Path == "/api/access/telegram" && r.Method == "GET":
		events := map[string]bool{}
		for _, kind := range notify.Events {
			events[kind] = h.Notify.Enabled(ctx, kind)
		}
		bot, err := h.Notify.Bot.Name(ctx)
		status := map[string]any{"configured": err == nil, "bot": bot, "events": events}
		if err != nil && !errors.Is(err, notify.ErrNoBot) {
			status["error"] = "bot_unavailable"
		}
		jsonResponse(rw, 200, status)
	case r.URL.Path == "/api/access/telegram" && r.Method == "POST":
		var in struct {
			Token  string `json:"token"`
			Remove bool   `json:"remove"`
		}
		if decodeLimited(rw, r, &in, 1024) != nil {
			apiError(rw, domain.ErrInvalid)
			return
		}
		if in.Remove {
			if err := h.Notify.Bot.RemoveToken(); err != nil {
				apiError(rw, err)
				return
			}
			jsonResponse(rw, 200, map[string]any{"configured": false})
			return
		}
		name, err := h.Notify.Bot.SetToken(ctx, in.Token)
		if err != nil {
			jsonResponse(rw, 400, map[string]string{"error": "invalid_token"})
			return
		}
		jsonResponse(rw, 200, map[string]any{"configured": true, "bot": name})
	case r.URL.Path == "/api/access/telegram/events" && r.Method == "POST":
		var in struct {
			Event   string `json:"event"`
			Enabled bool   `json:"enabled"`
		}
		if decodeLimited(rw, r, &in, 1024) != nil || h.Notify.SetEnabled(ctx, in.Event, in.Enabled) != nil {
			apiError(rw, domain.ErrInvalid)
			return
		}
		jsonResponse(rw, 200, map[string]bool{in.Event: in.Enabled})
	case r.URL.Path == "/api/access/telegram/test" && r.Method == "POST":
		delivered, err := h.Notify.Test(ctx)
		if err != nil {
			jsonResponse(rw, 502, map[string]string{"error": "not_delivered"})
			return
		}
		jsonResponse(rw, 200, map[string]int{"delivered": delivered})
	default:
		http.NotFound(rw, r)
	}
}

// approveRequest issues a read key for the requested sources and marks the
// request approved; the key is shown once, as on key creation.
func (h handler) approveRequest(rw http.ResponseWriter, r *http.Request) {
	var in struct {
		ID      string `json:"id"`
		Expires string `json:"expires_at"`
	}
	if decodeLimited(rw, r, &in, 1024) != nil {
		apiError(rw, domain.ErrInvalid)
		return
	}
	request, err := h.Notify.Store.Request(r.Context(), in.ID)
	if err != nil {
		apiError(rw, err)
		return
	}
	if request.State != "pending" {
		apiError(rw, domain.ErrConflict)
		return
	}
	public := map[string]bool{}
	for _, source := range h.Store.sources(true) {
		public[source.ID] = true
	}
	for _, id := range request.Sources {
		if !public[id] {
			// A source made private after the request is never granted this way.
			jsonResponse(rw, http.StatusConflict, map[string]string{"error": "source_not_public"})
			return
		}
	}
	expires := time.Now().Add(defaultRequestKeyDays * 24 * time.Hour)
	if in.Expires != "" {
		if expires, err = time.Parse(time.RFC3339, in.Expires); err != nil {
			apiError(rw, domain.ErrInvalid)
			return
		}
	}
	keyID := h.freeKeyID("tg-" + strings.ToLower(strings.TrimPrefix(request.Telegram, "@")))
	key, err := h.Store.auth().CreateAccessKey(keyID, request.Sources, expires)
	if err != nil {
		apiError(rw, err)
		return
	}
	if err = h.Notify.Store.Decide(r.Context(), request.ID, "approved", keyID, time.Now()); err != nil {
		_ = h.Store.auth().RevokeAccessKey(keyID)
		apiError(rw, err)
		return
	}
	jsonResponse(rw, 201, map[string]any{"id": keyID, "sources": request.Sources, "expires_at": expires.UTC().Format(time.RFC3339), "key": key})
}

// freeKeyID returns base, or base with a numeric suffix, not used by a key.
func (h handler) freeKeyID(base string) string {
	taken := map[string]bool{}
	for _, key := range h.Store.auth().ListAccessKeys() {
		taken[key.ID] = true
	}
	id := base
	for n := 2; taken[id]; n++ {
		id = base + "-" + string(rune('0'+n%10))
		if n >= 10 {
			id = base + "-" + randomID()[:6]
		}
	}
	return id
}
