package auth

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	if !ok || !s.AdminSessionValid("admin", version) || storetest.Read(t, s.Store).Version != 20 {
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

func TestOlderBinaryRefusesAdministratorStore(t *testing.T) {
	old := os.Getenv("VEDA_PRE_ADMIN_BINARY")
	if old == "" {
		t.Skip("pre-administrator binary not provided")
	}
	for _, sqlite := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "sqlite"}[sqlite], func(t *testing.T) {
			s := Service{Store: storetest.New(t)}
			storetest.Source(t, s.Store, "fixture", "private")
			if sqlite {
				if err := store.MigrateSQLite(s.Store.Dir()); err != nil {
					t.Fatal(err)
				}
				var err error
				s, err = reopen(s.Store.Dir())
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.BootstrapAdmin("operator"); err != nil {
				t.Fatal(err)
			}
			name := "state.json"
			if sqlite {
				name = "state.sqlite"
			}
			file := filepath.Join(s.Store.Dir(), name)
			before, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			output, err := exec.CommandContext(ctx, old, "--data", s.Store.Dir(), "--listen", "127.0.0.1:0", "--public-listen", "127.0.0.1:0").CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || ctx.Err() != nil || !strings.Contains(string(output), "unsupported store") {
				t.Fatal("old binary did not refuse administrator store")
			}
			after, err := os.ReadFile(file)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("old binary changed store")
			}
		})
	}
}
