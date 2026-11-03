package auth

import (
	"context"
	"crypto/sha256"
	"fmt"
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

func TestOldBinaryRejectsHumanStore(t *testing.T) {
	old := os.Getenv("VEDA_OLD_PEOPLE_BINARY")
	if old == "" {
		t.Skip("old binary not provided")
	}
	for _, sqlite := range []bool{false, true} {
		t.Run(fmt.Sprint("sqlite=", sqlite), func(t *testing.T) {
			s := Service{Store: storetest.New(t)}
			storetest.Source(t, s.Store, "alpha", "private")
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
			if _, err := s.SaveTeam("ops", []string{"alpha"}, nil); err != nil {
				t.Fatal(err)
			}
			groups, password := []string{"ops"}, "synthetic test password"
			if _, err := s.SaveUser("person", &groups, &password, nil, nil); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(s.Store.Dir(), "state.json")
			if sqlite {
				file = filepath.Join(s.Store.Dir(), store.SQLiteFile)
			}
			before, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			out, err := exec.CommandContext(ctx, old, "--data", s.Store.Dir(), "--listen", "127.0.0.1:0", "--public-listen", "127.0.0.1:0").CombinedOutput()
			if err == nil || ctx.Err() != nil || (!strings.Contains(string(out), "unsupported store") && !strings.Contains(string(out), "unknown SQLite record category")) {
				t.Fatal("old binary accepted human store")
			}
			after, err := os.ReadFile(file)
			if err != nil || sha256.Sum256(before) != sha256.Sum256(after) {
				t.Fatal("old binary changed data", err)
			}
		})
	}
}

// The browser access-management path cannot remove the last active
// administrator. Owner provisioning (setUserAdmin) remains the recovery path.
func TestLastActiveAdministratorCannotBeDisabled(t *testing.T) {
	s := Service{Store: storetest.New(t)}
	password := "синтетический пароль администратора"
	if err := s.CreateSuperuser("first-admin", password); err != nil {
		t.Fatal(err)
	}
	disabled := true
	if _, err := s.SaveUser("first-admin", nil, nil, &disabled, intPointer(1)); err != domain.ErrConflict {
		t.Fatal("last active administrator disabled", err)
	}
	if err := s.CreateSuperuser("second-admin", password); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveUser("first-admin", nil, nil, &disabled, intPointer(1)); err != nil {
		t.Fatal("administrator with an active peer could not be disabled", err)
	}
	if _, err := s.SaveUser("second-admin", nil, nil, &disabled, intPointer(1)); err != domain.ErrConflict {
		t.Fatal("remaining administrator disabled", err)
	}
}

func intPointer(v int) *int { return &v }
