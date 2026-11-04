package auth

import (
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store"
	"github.com/Mesteriis/veda-wiki/internal/store/storetest"
)

func TestAdministratorRoleInvalidatesOldSessionsAndSurvivesReopen(t *testing.T) {
	s := Service{Store: storetest.New(t)}
	groups := []string{}
	password := "synthetic password"
	user, err := s.SaveUser("admin", &groups, &password, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	oldVersion, ok := s.AuthenticateUser("admin", password)
	if !ok || s.AdminSessionValid("admin", oldVersion) {
		t.Fatal("reader is administrator")
	}
	if err = s.SetUserAdmin("admin", true, user.Revision); err != nil {
		t.Fatal(err)
	}
	if s.AdminSessionValid("admin", oldVersion) || s.UserSessionValid("admin", oldVersion) {
		t.Fatal("old session survived role change")
	}
	version, ok := s.AuthenticateUser("admin", password)
	if !ok || !s.AdminSessionValid("admin", version) || storetest.Read(t, s.Store).Version != domain.RevisionsFormat {
		t.Fatal("administrator not active")
	}
	reopened, err := reopen(s.Store.Dir())
	if err != nil || !reopened.AdminSessionValid("admin", version) {
		t.Fatal("role did not survive reopen", err)
	}
	if err = reopened.SetUserAdmin("admin", false, user.Revision); err != domain.ErrConflict {
		t.Fatal("stale role update accepted", err)
	}
	if err = reopened.SetUserAdmin("admin", false, user.Revision+1); err != nil {
		t.Fatal(err)
	}
	if reopened.AdminSessionValid("admin", version) {
		t.Fatal("revoked role still active")
	}
}

func TestAdminSetupExpiredTokenRejected(t *testing.T) {
	s := Service{Store: storetest.New(t)}
	token, err := s.BootstrapAdmin("operator")
	if err != nil {
		t.Fatal(err)
	}
	storetest.Mutate(t, s.Store, func(db *domain.Database) {
		u := db.Users["operator"]
		u.SetupExpires = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
		db.Users["operator"] = u
	})
	if err := s.FinishAdminSetup("operator", token, "synthetic long password"); err == nil {
		t.Fatal("expired setup token accepted")
	}
}

func TestAdminBootstrapSurvivesSQLiteRestart(t *testing.T) {
	s := Service{Store: storetest.New(t)}
	storetest.Source(t, s.Store, "fixture", "private")
	if err := store.MigrateSQLite(s.Store.Dir()); err != nil {
		t.Fatal(err)
	}
	s, err := reopen(s.Store.Dir())
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.BootstrapAdmin("operator")
	if err != nil {
		t.Fatal(err)
	}
	s, err = reopen(s.Store.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.FinishAdminSetup("operator", token, "synthetic password"); err != nil {
		t.Fatal(err)
	}
	s, err = reopen(s.Store.Dir())
	if err != nil {
		t.Fatal(err)
	}
	v, ok := s.AuthenticateUser("operator", "synthetic password")
	if !ok || !s.AdminSessionValid("operator", v) {
		t.Fatal("admin login not restored")
	}
}
