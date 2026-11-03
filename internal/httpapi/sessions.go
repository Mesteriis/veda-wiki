package httpapi

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/auth"
	"github.com/Mesteriis/veda-wiki/internal/domain"
)

func (h handler) adminCookieValid(r *http.Request, sessions *auth.Sessions, cookie string) bool {
	if sessions.ValidCookie(r, cookie) {
		return true
	}
	u, ok := sessions.NamedUserCookie(r, cookie)
	if ok && h.Store != nil && h.Store.auth().AdminSessionValid(u.ID, u.AuthVersion) {
		return true
	}
	return h.readerAdminCookieValid(r)
}

func (h handler) readerAdminCookieValid(r *http.Request) bool {
	u, ok := h.Sessions.UserCookie(r)
	return ok && h.Store != nil && h.Store.auth().AdminSessionValid(u.ID, u.AuthVersion)
}

func (h handler) sessionAPI(rw http.ResponseWriter, r *http.Request) {
	sessions, cookie, token, prefix := h.Sessions, auth.ReaderCookie, h.ReaderToken, "/api/session/"
	if strings.HasPrefix(r.URL.Path, "/api/admin/session/") {
		sessions, cookie, token, prefix = h.AdminSessions, auth.AdminCookie, h.OwnerToken, "/api/admin/session/"
	}
	if strings.HasPrefix(r.URL.Path, "/api/access/session/") {
		sessions, cookie, token, prefix = h.AccessSessions, auth.AccessCookie, h.OwnerToken, "/api/access/session/"
	}
	if h.Public || sessions == nil || !auth.SecureBrowserRequest(r) {
		http.NotFound(rw, r)
		return
	}
	if r.Method == "GET" && r.URL.Path == prefix+"status" {
		valid := sessions.ValidCookie(r, cookie)
		if prefix != "/api/session/" {
			valid = h.adminCookieValid(r, sessions, cookie)
		}
		if !valid && prefix == "/api/session/" && h.Store != nil {
			if hash, ok := sessions.ScopedCookie(r); ok {
				valid = h.Store.auth().ValidScopeHash(hash, time.Now())
			}
			if user, ok := sessions.UserCookie(r); ok {
				valid = h.Store.auth().UserSessionValid(user.ID, user.AuthVersion)
			}
		}
		result := map[string]bool{"authenticated": valid}
		if prefix != "/api/session/" {
			result["via_reader_session"] = valid && h.readerAdminCookieValid(r)
		}
		jsonResponse(rw, 200, result)
		return
	}
	if r.Method == "GET" && prefix == "/api/session/" && r.URL.Path == prefix+"profile" {
		user, ok := sessions.UserCookie(r)
		jsonResponse(rw, 200, map[string]bool{"password_change": ok && h.Store != nil && h.Store.auth().UserSessionValid(user.ID, user.AuthVersion)})
		return
	}
	if r.Method != "POST" {
		rw.WriteHeader(405)
		return
	}
	if !auth.SameOrigin(r) {
		jsonResponse(rw, 403, map[string]string{"error": "origin rejected"})
		return
	}
	if prefix == "/api/admin/session/" && r.URL.Path == prefix+"setup" {
		var in struct {
			Username string `json:"username"`
			Token    string `json:"setup_token"`
			Password string `json:"password"`
		}
		if decodeLimited(rw, r, &in, 2048) != nil {
			apiError(rw, domain.ErrInvalid)
			return
		}
		if !sessions.AllowAttempt("user", in.Username, time.Now()) {
			jsonResponse(rw, 429, map[string]string{"error": "try again later"})
			return
		}
		if h.Store == nil {
			http.NotFound(rw, r)
			return
		}
		if err := h.Store.auth().FinishAdminSetup(in.Username, in.Token, in.Password); err != nil {
			apiError(rw, err)
			return
		}
		jsonResponse(rw, 200, map[string]bool{"configured": true})
		return
	}
	if r.URL.Path == prefix+"logout" {
		clear := func(store *auth.Sessions, name string) {
			if store != nil {
				if c, e := r.Cookie(name); e == nil {
					store.Revoke(c.Value)
				}
			}
			auth.ClearCookie(rw, r, name)
		}
		clear(sessions, cookie)
		if prefix == "/api/session/" {
			clear(h.AdminSessions, auth.AdminCookie)
			clear(h.AccessSessions, auth.AccessCookie)
		}
		jsonResponse(rw, 200, map[string]bool{"authenticated": false})
		return
	}
	if prefix == "/api/session/" && r.URL.Path == prefix+"password" {
		user, ok := sessions.UserCookie(r)
		if !ok || h.Store == nil {
			http.NotFound(rw, r)
			return
		}
		var in struct {
			Current string `json:"current_password"`
			New     string `json:"new_password"`
		}
		if decodeLimited(rw, r, &in, 2048) != nil {
			apiError(rw, domain.ErrInvalid)
			return
		}
		if !sessions.AllowAttempt("user", user.ID, time.Now()) {
			jsonResponse(rw, 429, map[string]string{"error": "try again later"})
			return
		}
		version, valid := h.Store.auth().AuthenticateUser(user.ID, in.Current)
		if !valid || version != user.AuthVersion {
			jsonResponse(rw, 403, map[string]string{"error": "access denied"})
			return
		}
		if err := h.Store.auth().ChangePassword(user.ID, user.AuthVersion, in.New); err != nil {
			apiError(rw, err)
			return
		}
		if c, e := r.Cookie(cookie); e == nil {
			sessions.Revoke(c.Value)
		}
		auth.ClearCookie(rw, r, cookie)
		jsonResponse(rw, 200, map[string]bool{"authenticated": false})
		return
	}
	if r.URL.Path != prefix+"login" {
		http.NotFound(rw, r)
		return
	}
	var in struct {
		Key      string `json:"key"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if decodeLimited(rw, r, &in, 2048) != nil {
		apiError(rw, domain.ErrInvalid)
		return
	}
	now := time.Now()
	keyAttempt := in.Username == "" && in.Password == ""
	authenticate := func() (auth.Grant, bool) {
		if in.Username == "" && in.Password == "" && token != "" && subtle.ConstantTimeCompare([]byte(in.Key), []byte(token)) == 1 {
			return auth.Grant{}, true
		}
		if in.Username == "" && in.Password == "" && prefix == "/api/session/" && h.Store != nil && h.Store.auth().AccessScope(in.Key, now) != nil {
			return auth.Grant{Scoped: true, Scope: sha256.Sum256([]byte(in.Key))}, true
		}
		if in.Key == "" && h.Store != nil {
			person := auth.SessionUser{ID: in.Username}
			var userAccess bool
			person.AuthVersion, userAccess = h.Store.auth().AuthenticateUser(in.Username, in.Password)
			if userAccess && prefix != "/api/session/" {
				userAccess = h.Store.auth().AdminSessionValid(person.ID, person.AuthVersion)
			}
			return auth.Grant{Named: true, User: person}, userAccess
		}
		return auth.Grant{}, false
	}
	current := func(g auth.Grant) bool {
		if h.Store == nil {
			return false
		}
		if g.Scoped {
			return h.Store.auth().ValidScopeHash(g.Scope, now)
		}
		return h.Store.auth().UserSessionValid(g.User.ID, g.User.AuthVersion)
	}
	value, err := sessions.Issue(auth.Login{Now: now, Limited: !keyAttempt, Kind: "user", Identity: in.Username, Authenticate: authenticate, Current: current})
	switch {
	case errors.Is(err, auth.ErrRateLimited):
		jsonResponse(rw, 429, map[string]string{"error": "try again later"})
		return
	case errors.Is(err, auth.ErrDenied):
		jsonResponse(rw, 403, map[string]string{"error": "access denied"})
		return
	case errors.Is(err, auth.ErrSessionLimit):
		jsonResponse(rw, 429, map[string]string{"error": "session limit"})
		return
	case err != nil:
		apiError(rw, err)
		return
	}
	auth.SetCookie(rw, r, cookie, value)
	jsonResponse(rw, 200, map[string]bool{"authenticated": true})
}
