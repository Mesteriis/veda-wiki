package auth

import (
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store/storetest"
)

func TestPasswordMinimumTenCharacters(t *testing.T) {
	if PasswordValid("123456789") || !PasswordValid("1234567890") || !PasswordValid("абвгдежзий") {
		t.Fatal("incorrect minimum password length")
	}
	s := Service{Store: storetest.New(t)}
	if err := s.CreateSuperuser("short", "123456789"); err != domain.ErrInvalid {
		t.Fatal("nine-character password accepted")
	}
	if err := s.CreateSuperuser("valid", "1234567890"); err != nil {
		t.Fatal("ten-character password refused", err)
	}
	if _, ok := s.AuthenticateUser("valid", "1234567890"); !ok {
		t.Fatal("ten-character password cannot authenticate")
	}
}
