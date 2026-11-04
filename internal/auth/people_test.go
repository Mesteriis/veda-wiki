package auth

import (
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store/storetest"
)

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
