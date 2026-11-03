package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store"
	"github.com/Mesteriis/veda-wiki/internal/store/storetest"
)

func TestAccessKeyScopePersistenceExpiryAndRevocation(t *testing.T) {
	s := Service{Store: storetest.New(t)}
	storetest.Source(t, s.Store, "alpha", "private")
	storetest.Source(t, s.Store, "beta", "private")
	expiry := time.Now().Add(time.Hour)
	key, err := s.CreateAccessKey("agent", []string{"alpha"}, expiry)
	if err != nil {
		t.Fatal(err)
	}
	state, err := os.ReadFile(filepath.Join(s.Store.Dir(), "state.json"))
	if err != nil || strings.Contains(string(state), key) {
		t.Fatal("bearer token was stored", err)
	}
	reopened, err := reopen(s.Store.Dir())
	if err != nil || storetest.Read(t, reopened.Store).Version != 4 {
		t.Fatal("key store did not persist", err)
	}
	scope := reopened.AccessScope(key, time.Now())
	if len(scope) != 1 || scope[0] != "alpha" {
		t.Fatal("wrong scope")
	}
	if reopened.AccessScope(key, expiry.Add(time.Second)) != nil {
		t.Fatal("expired key accepted")
	}
	if reopened.AccessScope(strings.Repeat("0", 64), time.Now()) != nil {
		t.Fatal("invalid key accepted")
	}
	if err := reopened.RevokeAccessKey("agent"); err != nil {
		t.Fatal(err)
	}
	if reopened.AccessScope(key, time.Now()) != nil {
		t.Fatal("revoked key accepted")
	}
	reopened, err = reopen(s.Store.Dir())
	if err != nil || reopened.AccessScope(key, time.Now()) != nil {
		t.Fatal("revocation not durable", err)
	}
}

func TestAccessKeyCreationRejectsInvalidScope(t *testing.T) {
	s := Service{Store: storetest.New(t)}
	storetest.Source(t, s.Store, "alpha", "private")
	for _, scope := range [][]string{nil, {"missing"}, {"alpha", "alpha"}} {
		if _, err := s.CreateAccessKey("agent", scope, time.Now().Add(time.Hour)); err != domain.ErrInvalid {
			t.Fatalf("scope accepted: %v", err)
		}
	}
	if _, err := s.CreateAccessKey("agent", []string{"alpha"}, time.Now().Add(-time.Second)); err != domain.ErrInvalid {
		t.Fatal("expired key created", err)
	}
	if storetest.Read(t, s.Store).Version != 1 || len(storetest.Read(t, s.Store).AccessKeys) != 0 {
		t.Fatal("invalid request mutated store")
	}
}

// reopen opens the store in dir again, as a restarted process would.
func reopen(dir string) (Service, error) {
	st, err := store.Open(dir)
	return Service{Store: st}, err
}
