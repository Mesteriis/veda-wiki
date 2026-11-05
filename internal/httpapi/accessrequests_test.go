package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/auth"
	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/notify"
)

func notifyService(t *testing.T, s *Store) *notify.Service {
	t.Helper()
	db, err := notify.Open(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &notify.Service{Store: db, Bot: notify.NewBot(s.Dir())}
}

func formRequest(w Wiki, body, peer, realIP string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "http://localhost:8080/api/public/access-requests", strings.NewReader(body))
	r.Header.Set("Origin", "http://localhost:8080")
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = peer + ":5000"
	if realIP != "" {
		r.Header.Set("X-Real-IP", realIP)
	}
	rw := httptest.NewRecorder()
	w.ServeHTTP(rw, r)
	return rw
}

func TestPublicAccessRequestAcceptsOnlyValidRequestsForPublicSources(t *testing.T) {
	s := testStore(t)
	source(t, s, "guide", "public")
	source(t, s, "private", "private")
	n := notifyService(t, s)
	w := Wiki{Store: s, Public: true, Notify: n}
	ok := `{"sources":["guide"],"telegram":"@person_1","comment":"agent for docs"}`
	if got := formRequest(w, ok, "198.51.100.7", ""); got.Code != http.StatusAccepted {
		t.Fatalf("valid request: %d %s", got.Code, got.Body.String())
	}
	for name, body := range map[string]string{
		"private source": `{"sources":["private"],"telegram":"@person_1"}`,
		"unknown source": `{"sources":["missing"],"telegram":"@person_1"}`,
		"no sources":     `{"sources":[],"telegram":"@person_1"}`,
		"bad nick":       `{"sources":["guide"],"telegram":"person"}`,
		"long comment":   `{"sources":["guide"],"telegram":"@person_1","comment":"` + strings.Repeat("a", notify.MaxComment+1) + `"}`,
	} {
		if got := formRequest(w, body, "198.51.100.8", ""); got.Code != 400 {
			t.Fatalf("%s: %d", name, got.Code)
		}
	}
	if got := formRequest(w, `{"sources":["guide"],"telegram":"@bot_bot","website":"http://spam"}`, "198.51.100.9", ""); got.Code != http.StatusAccepted {
		t.Fatalf("honeypot answer: %d", got.Code)
	}
	r := httptest.NewRequest("POST", "http://localhost:8080/api/public/access-requests", strings.NewReader(ok))
	r.Header.Set("Origin", "http://evil.example")
	rw := httptest.NewRecorder()
	w.ServeHTTP(rw, r)
	if rw.Code != 403 {
		t.Fatalf("cross-origin request: %d", rw.Code)
	}
	list, err := n.Store.Requests(t.Context())
	if err != nil || len(list) != 1 || list[0].Telegram != "@person_1" || list[0].Comment != "agent for docs" {
		t.Fatalf("stored: %+v %v", list, err)
	}
}

func TestPublicAccessRequestLimitsPerVisitorBehindATrustedProxy(t *testing.T) {
	s := testStore(t)
	source(t, s, "guide", "public")
	w := Wiki{Store: s, Public: true, Notify: notifyService(t, s), TrustedProxies: []string{"10.0.0.7"}}
	body := `{"sources":["guide"],"telegram":"@person_1"}`
	for i := 0; i < notify.PerAddressPerHour; i++ {
		if got := formRequest(w, body, "10.0.0.7", "203.0.113.5"); got.Code != http.StatusAccepted {
			t.Fatalf("attempt %d: %d", i, got.Code)
		}
	}
	if got := formRequest(w, body, "10.0.0.7", "203.0.113.5"); got.Code != http.StatusTooManyRequests {
		t.Fatalf("limit through the proxy: %d", got.Code)
	}
	if got := formRequest(w, body, "10.0.0.7", "203.0.113.6"); got.Code != http.StatusAccepted {
		t.Fatalf("another visitor behind the proxy: %d", got.Code)
	}
	// X-Real-IP from an untrusted peer is ignored: the peer itself is limited.
	for i := 0; i < notify.PerAddressPerHour; i++ {
		formRequest(w, body, "198.51.100.20", "203.0.113."+string(rune('a'+i)))
	}
	if got := formRequest(w, body, "198.51.100.20", "203.0.113.99"); got.Code != http.StatusTooManyRequests {
		t.Fatalf("spoofed X-Real-IP bypassed the limit: %d", got.Code)
	}
}

func TestAccessAdministratorIssuesAKeyForARequest(t *testing.T) {
	s := testStore(t)
	source(t, s, "guide", "public")
	n := notifyService(t, s)
	groups := []string{}
	password := "synthetic password"
	user, err := s.auth().SaveUser("admin", &groups, &password, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.auth().SetUserAdmin("admin", true, user.Revision); err != nil {
		t.Fatal(err)
	}
	w := Wiki{Store: s, Token: "owner", Notify: n, AdminSessions: &auth.Sessions{}, AccessSessions: &auth.Sessions{}, Sessions: &auth.Sessions{}}
	if got := formRequest(w, `{"sources":["guide"],"telegram":"@Person_1","comment":"hi"}`, "198.51.100.7", ""); got.Code != http.StatusAccepted {
		t.Fatalf("request: %d", got.Code)
	}
	call := func(method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://localhost:8080"+path, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1234"
		if method == "POST" {
			r.Header.Set("Origin", "http://localhost:8080")
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		out := httptest.NewRecorder()
		w.ServeHTTP(out, r)
		return out
	}
	if got := call("GET", "/api/access/requests", "", nil); got.Code != 404 {
		t.Fatalf("requests without a session: %d", got.Code)
	}
	login := call("POST", "/api/access/session/login", `{"username":"admin","password":"synthetic password"}`, nil)
	if login.Code != 200 {
		t.Fatalf("access login: %d", login.Code)
	}
	cookie := login.Result().Cookies()[0]
	listed := call("GET", "/api/access/requests", "", cookie)
	var requests []notify.Request
	if listed.Code != 200 || json.Unmarshal(listed.Body.Bytes(), &requests) != nil || len(requests) != 1 {
		t.Fatalf("list: %d %s", listed.Code, listed.Body.String())
	}
	approved := call("POST", "/api/access/requests/approve", `{"id":"`+requests[0].ID+`"}`, cookie)
	var issued struct {
		ID      string   `json:"id"`
		Key     string   `json:"key"`
		Sources []string `json:"sources"`
	}
	if approved.Code != 201 || json.Unmarshal(approved.Body.Bytes(), &issued) != nil || issued.ID != "tg-person_1" || len(issued.Key) != 64 || issued.Sources[0] != "guide" {
		t.Fatalf("approve: %d %s", approved.Code, approved.Body.String())
	}
	if again := call("POST", "/api/access/requests/approve", `{"id":"`+requests[0].ID+`"}`, cookie); again.Code != 409 {
		t.Fatalf("a request was approved twice: %d", again.Code)
	}
	if _, valid, _ := s.auth().ScopedView(issued.Key, time.Now()); !valid {
		t.Fatal("the issued key does not work")
	}
	status := call("GET", "/api/access/telegram", "", cookie)
	if status.Code != 200 || !strings.Contains(status.Body.String(), `"configured":false`) {
		t.Fatalf("telegram status: %d %s", status.Code, status.Body.String())
	}
	if bad := call("POST", "/api/access/telegram", `{"token":"nope"}`, cookie); bad.Code != 400 {
		t.Fatalf("bad token: %d", bad.Code)
	}
	if toggled := call("POST", "/api/access/telegram/events", `{"event":"keys","enabled":false}`, cookie); toggled.Code != 200 || n.Enabled(t.Context(), notify.EventKeys) {
		t.Fatalf("event toggle: %d", toggled.Code)
	}
	userLogin := call("POST", "/api/session/login", `{"username":"admin","password":"synthetic password"}`, nil)
	if userLogin.Code != 200 {
		t.Fatalf("reader login: %d", userLogin.Code)
	}
	userCookie := userLogin.Result().Cookies()[0]
	if set := call("POST", "/api/session/telegram", `{"telegram_id":123456789}`, userCookie); set.Code != 200 {
		t.Fatalf("telegram id: %d %s", set.Code, set.Body.String())
	}
	profile := call("GET", "/api/session/profile", "", userCookie)
	if !strings.Contains(profile.Body.String(), `"telegram_id":123456789`) {
		t.Fatalf("profile: %s", profile.Body.String())
	}
}

func TestApprovalRefusesASourceMadePrivateAndCrossOriginForms(t *testing.T) {
	s := testStore(t)
	source(t, s, "guide", "public")
	n := notifyService(t, s)
	w := Wiki{Store: s, Public: true, Notify: n}
	if got := formRequest(w, `{"sources":["guide"],"telegram":"@person_1"}`, "198.51.100.7", ""); got.Code != http.StatusAccepted {
		t.Fatalf("request: %d", got.Code)
	}
	r := httptest.NewRequest("POST", "http://localhost:8080/api/public/access-requests", strings.NewReader(`{"sources":["guide"],"telegram":"@person_2"}`))
	rw := httptest.NewRecorder()
	w.ServeHTTP(rw, r)
	if rw.Code != 403 {
		t.Fatalf("a form without Origin was accepted: %d", rw.Code)
	}
	s.mutate(t, func(db *domain.Database) {
		src := db.Sources["guide"]
		src.Visibility = "private"
		db.Sources["guide"] = src
	})
	list, _ := n.Store.Requests(t.Context())
	owner := Wiki{Store: s, Token: "test-owner", Notify: n}
	approve := httptest.NewRequest("POST", "http://localhost:8080/api/access/requests/approve", strings.NewReader(`{"id":"`+list[0].ID+`"}`))
	rec := httptest.NewRecorder()
	owner.handler().approveRequest(rec, approve)
	if rec.Code != http.StatusConflict {
		t.Fatalf("a request for a now-private source was approved: %d %s", rec.Code, rec.Body.String())
	}
	if len(s.auth().ListAccessKeys()) != 0 {
		t.Fatal("a key was created for a private source")
	}
}

func TestClientAddressComparesProxyAddressesAsIPs(t *testing.T) {
	h := Wiki{TrustedProxies: []string{"::1"}}.handler()
	r := httptest.NewRequest("POST", "http://localhost/", nil)
	r.RemoteAddr = "[0:0:0:0:0:0:0:1]:5000"
	r.Header.Set("X-Real-IP", "203.0.113.9")
	if got := h.clientAddress(r); got != "203.0.113.9" {
		t.Fatalf("a trusted proxy written differently was not recognised: %q", got)
	}
}
