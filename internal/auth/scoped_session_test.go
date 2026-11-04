package auth

import (
	"crypto/sha256"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store"
	"github.com/Mesteriis/veda-wiki/internal/store/storetest"
)

func viewSources(t *testing.T, view *store.Store) []string {
	t.Helper()
	ids := []string{}
	for id := range storetest.Read(t, view).Sources {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func TestScopedViewsFollowKeyAndUserScope(t *testing.T) {
	s := Service{Store: storetest.New(t)}
	storetest.Source(t, s.Store, "alpha", "private")
	storetest.Source(t, s.Store, "beta", "private")
	key, err := s.CreateAccessKey("agent", []string{"alpha"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "short", strings.Repeat("z", 64), strings.Repeat("0", 64)} {
		if view, valid, err := s.ScopedView(bad, time.Now()); view != nil || valid || err != nil {
			t.Fatalf("token %q granted a view", bad)
		}
	}
	view, valid, err := s.ScopedView(key, time.Now())
	if err != nil || !valid || !slices.Equal(viewSources(t, view), []string{"alpha"}) {
		t.Fatal("key view has wrong sources", err)
	}
	if view.Update(func(*store.Tx) error { return nil }) == nil {
		t.Fatal("scoped view is writable")
	}
	if _, valid, _ := s.ScopedView(key, time.Now().Add(2*time.Hour)); valid {
		t.Fatal("expired key granted a view")
	}
	hashView, valid, err := s.ScopedViewHash(sha256.Sum256([]byte(key)), time.Now())
	if err != nil || !valid || !slices.Equal(viewSources(t, hashView), []string{"alpha"}) {
		t.Fatal("session view has wrong sources", err)
	}
	if err := s.RevokeAccessKey("agent"); err != nil {
		t.Fatal(err)
	}
	if _, valid, _ := s.ScopedViewHash(sha256.Sum256([]byte(key)), time.Now()); valid || s.ValidScopeHash(sha256.Sum256([]byte(key)), time.Now()) {
		t.Fatal("revoked key granted a view")
	}

	if _, err := s.SaveTeam("ops", []string{"beta"}, nil); err != nil {
		t.Fatal(err)
	}
	groups, password := []string{"ops"}, "synthetic test password"
	if _, err := s.SaveUser("person", &groups, &password, nil, nil); err != nil {
		t.Fatal(err)
	}
	version, ok := s.AuthenticateUser("person", password)
	if !ok {
		t.Fatal("user login failed")
	}
	userView, valid, err := s.ScopedViewUser("person", version)
	if err != nil || !valid || !slices.Equal(viewSources(t, userView), []string{"beta"}) {
		t.Fatal("user view has wrong sources", err)
	}
	if _, valid, _ := s.ScopedViewUser("person", version+1); valid {
		t.Fatal("stale session version granted a view")
	}
	disabled := true
	if _, err := s.SaveUser("person", nil, nil, &disabled, intPointer(1)); err != nil {
		t.Fatal(err)
	}
	if _, valid, _ := s.ScopedViewUser("person", version); valid || s.UserSessionValid("person", version) {
		t.Fatal("disabled user granted a view")
	}
}

// An administrator's session covers every source, including ones added after
// login; the scope is evaluated with the snapshot, not at login.
func TestAdministratorUserViewCoversAllSources(t *testing.T) {
	s := Service{Store: storetest.New(t)}
	storetest.Source(t, s.Store, "alpha", "private")
	password := "synthetic administrator password"
	if err := s.CreateSuperuser("admin", password); err != nil {
		t.Fatal(err)
	}
	version, ok := s.AuthenticateUser("admin", password)
	if !ok {
		t.Fatal("administrator login failed")
	}
	storetest.Mutate(t, s.Store, func(db *domain.Database) {
		db.Sources["beta"] = domain.Source{ID: "beta", Title: "beta", Kind: "wiki", Visibility: "private"}
	})
	view, valid, err := s.ScopedViewUser("admin", version)
	if err != nil || !valid || !slices.Equal(viewSources(t, view), []string{"alpha", "beta"}) {
		t.Fatal("administrator view has wrong sources", err)
	}
}
