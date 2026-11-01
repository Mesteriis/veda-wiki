package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAccessKeyScopePersistenceExpiryAndRevocation(t *testing.T) {
	s := testStore(t)
	source(t, s, "alpha", "private")
	source(t, s, "beta", "private")
	expiry := time.Now().Add(time.Hour)
	key, err := s.createAccessKey("agent", []string{"alpha"}, expiry)
	if err != nil {
		t.Fatal(err)
	}
	state, err := os.ReadFile(filepath.Join(s.dir, "state.json"))
	if err != nil || strings.Contains(string(state), key) {
		t.Fatal("bearer token was stored", err)
	}
	reopened, err := openStore(s.dir)
	if err != nil || reopened.db.Version != 4 {
		t.Fatal("key store did not persist", err)
	}
	scope := reopened.accessScope(key, time.Now())
	if len(scope) != 1 || scope[0] != "alpha" {
		t.Fatal("wrong scope")
	}
	if reopened.accessScope(key, expiry.Add(time.Second)) != nil {
		t.Fatal("expired key accepted")
	}
	if reopened.accessScope(strings.Repeat("0", 64), time.Now()) != nil {
		t.Fatal("invalid key accepted")
	}
	if err := reopened.revokeAccessKey("agent"); err != nil {
		t.Fatal(err)
	}
	if reopened.accessScope(key, time.Now()) != nil {
		t.Fatal("revoked key accepted")
	}
	reopened, err = openStore(s.dir)
	if err != nil || reopened.accessScope(key, time.Now()) != nil {
		t.Fatal("revocation not durable", err)
	}
}

func TestAccessKeyCreationRejectsInvalidScope(t *testing.T) {
	s := testStore(t)
	source(t, s, "alpha", "private")
	for _, scope := range [][]string{nil, {"missing"}, {"alpha", "alpha"}} {
		if _, err := s.createAccessKey("agent", scope, time.Now().Add(time.Hour)); err != ErrInvalid {
			t.Fatalf("scope accepted: %v", err)
		}
	}
	if _, err := s.createAccessKey("agent", []string{"alpha"}, time.Now().Add(-time.Second)); err != ErrInvalid {
		t.Fatal("expired key created", err)
	}
	if s.db.Version != 1 || len(s.db.AccessKeys) != 0 {
		t.Fatal("invalid request mutated store")
	}
}
