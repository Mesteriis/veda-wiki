package main

import (
	"encoding/json"
	"fmt"
	"github.com/Mesteriis/veda-wiki/internal/auth"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAccessKeyScopeUpdateAppliesToExistingSessionAndBearer(t *testing.T) {
	s := testStore(t)
	for _, id := range []string{"alpha", "beta"} {
		source(t, s, id, "private")
		if _, err := s.put(id, "guide.md", "# "+id, 0); err != nil {
			t.Fatal(err)
		}
	}
	expires := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	key, err := s.auth().CreateAccessKey("person", []string{"alpha"}, expires)
	if err != nil {
		t.Fatal(err)
	}
	etag := s.auth().ListAccessKeys()[0].ETag
	w := Wiki{Store: s, Token: "owner", Sessions: &auth.Sessions{}, AccessSessions: &auth.Sessions{}}
	call := func(method, path, body, origin, bearer string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://localhost:8080"+path, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1234"
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		out := httptest.NewRecorder()
		w.ServeHTTP(out, r)
		return out
	}
	origin := "http://localhost:8080"
	login := call("POST", "/api/session/login", fmt.Sprintf(`{"key":%q}`, key), origin, "", nil)
	if login.Code != 200 || len(login.Result().Cookies()) != 1 {
		t.Fatal("reader login failed", login.Code)
	}
	cookie := login.Result().Cookies()[0]
	if call("GET", "/api/v1/articles?source_id=alpha&path=guide.md", "", "", "", cookie).Code != 200 {
		t.Fatal("initial access missing")
	}
	payload := fmt.Sprintf(`{"id":"person","sources":["beta"],"expires_at":%q,"expected_etag":%q}`, expires.Format(time.RFC3339Nano), etag)
	if call("POST", "/api/v1/access-keys", payload, "", key, nil).Code != 403 {
		t.Fatal("reader modified own access")
	}
	if call("POST", "/api/v1/access-keys", payload, origin, "owner", nil).Code != 403 {
		t.Fatal("browser origin bypassed owner API")
	}
	updated := call("POST", "/api/v1/access-keys", payload, "", "owner", nil)
	if updated.Code != 200 || strings.Contains(updated.Body.String(), key) {
		t.Fatal("update failed or disclosed bearer", updated.Code)
	}
	var result auth.AccessKeyView
	if json.Unmarshal(updated.Body.Bytes(), &result) != nil || result.ETag == etag {
		t.Fatal("fingerprint not updated")
	}
	for _, auth := range []struct {
		bearer string
		cookie *http.Cookie
	}{{key, nil}, {"", cookie}} {
		if call("GET", "/api/v1/articles?source_id=alpha&path=guide.md", "", "", auth.bearer, auth.cookie).Code != 404 {
			t.Fatal("old source remained accessible")
		}
		if call("GET", "/api/v1/articles?source_id=beta&path=guide.md", "", "", auth.bearer, auth.cookie).Code != 200 {
			t.Fatal("new source unavailable")
		}
	}
	if call("POST", "/api/v1/access-keys", payload, "", "owner", nil).Code != 409 {
		t.Fatal("stale update accepted")
	}
	if err = migrateSQLite(s.Dir()); err != nil {
		t.Fatal(err)
	}
	reopened, err := openStore(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if scope := reopened.auth().AccessScope(key, time.Now()); len(scope) != 1 || scope[0] != "beta" {
		t.Fatal("updated scope not durable")
	}
	old := reopened.auth().ListAccessKeys()[0].ETag
	if err = reopened.auth().RevokeAccessKey("person"); err != nil {
		t.Fatal(err)
	}
	if _, err = reopened.auth().CreateAccessKey("person", []string{"beta"}, expires); err != nil {
		t.Fatal(err)
	}
	if _, err = reopened.auth().UpdateAccessKey("person", []string{"alpha"}, expires, old); err != ErrConflict {
		t.Fatal("stale fingerprint matched reissued key", err)
	}
}

func TestConcurrentAccessKeyUpdatesHaveOneWinner(t *testing.T) {
	s := testStore(t)
	for _, id := range []string{"alpha", "beta", "gamma"} {
		source(t, s, id, "private")
	}
	expiry := time.Now().Add(time.Hour)
	if _, err := s.auth().CreateAccessKey("person", []string{"alpha"}, expiry); err != nil {
		t.Fatal(err)
	}
	etag := s.auth().ListAccessKeys()[0].ETag
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, source := range []string{"beta", "gamma"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.auth().UpdateAccessKey("person", []string{source}, expiry, etag)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if err == ErrConflict {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("concurrent updates were not serialized", success, conflict)
	}
}

func TestExpiredAccessKeyCannotBeRenewed(t *testing.T) {
	s := testStore(t)
	source(t, s, "alpha", "private")
	if _, err := s.auth().CreateAccessKey("person", []string{"alpha"}, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	s.Lock()
	db := s.Clone()
	key := db.AccessKeys["person"]
	key.Expires = time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)
	db.AccessKeys["person"] = key
	err := s.Commit(db)
	s.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	old := s.auth().ListAccessKeys()[0].ETag
	if _, err = s.auth().UpdateAccessKey("person", []string{"alpha"}, time.Now().Add(time.Hour), old); err != ErrConflict {
		t.Fatal("expired key was renewed", err)
	}
}
