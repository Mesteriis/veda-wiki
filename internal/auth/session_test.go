package auth

import (
	"strconv"
	"testing"
	"time"
)

func TestLoginAttemptTableIsBounded(t *testing.T) {
	s := &Sessions{}
	now := time.Now()
	for i := 0; i < attemptIdentityMax; i++ {
		if !s.AllowAttempt("user", strconv.Itoa(i), now) {
			t.Fatal("identity rejected before the table was full", i)
		}
	}
	if s.AllowAttempt("user", "new", now) {
		t.Fatal("full table accepted a new identity")
	}
	if !s.AllowAttempt("user", "0", now) {
		t.Fatal("full table locked out a known identity below its limit")
	}
	if !s.AllowAttempt("user", "new", now.Add(2*time.Minute)) || len(s.attempts) != 1 {
		t.Fatal("expired identities were not pruned", len(s.attempts))
	}
}

// A login without Current cannot confirm restricted sessions, so pruning
// treats scoped and user sessions as no longer valid instead of panicking.
func TestIssueWithoutCurrentPrunesRestrictedSessions(t *testing.T) {
	s := &Sessions{}
	now := time.Now()
	grants := []Grant{{Scoped: true, Scope: [32]byte{1}}, {Named: true, User: SessionUser{ID: "person"}}, {}}
	for _, grant := range grants {
		if _, err := s.Issue(Login{Now: now, Authenticate: func() (Grant, bool) { return grant, true }, Current: func(Grant) bool { return true }}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Issue(Login{Now: now, Authenticate: func() (Grant, bool) { return Grant{}, true }}); err != nil {
		t.Fatal(err)
	}
	if active, scoped := s.Count(); active != 2 || scoped != 0 || len(s.users) != 0 {
		t.Fatal("restricted sessions survived a login without Current", active, scoped, len(s.users))
	}
}
