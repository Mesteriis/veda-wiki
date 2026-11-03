package main

import (
	"context"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestCreateSuperuserRestrictedAndActivatesPending(t *testing.T) {
	s := testStore(t)
	token, err := s.bootstrapAdmin("operator")
	if err != nil {
		t.Fatal(err)
	}
	w := Wiki{Store: s, ReaderLocal: true, LocalImportSource: "infra"}
	body := `{"id":"operator","password":"synthetic long password"}`
	request := func(root bool, origin bool) int {
		r := httptest.NewRequest("POST", "http://localhost/api/local/createsuperuser", strings.NewReader(body))
		if root {
			r = r.WithContext(context.WithValue(r.Context(), localRootKey{}, true))
		}
		if origin {
			r.Header.Set("Origin", "http://localhost")
		}
		out := httptest.NewRecorder()
		w.ServeHTTP(out, r)
		return out.Code
	}
	if request(false, false) == 201 || request(true, true) == 201 {
		t.Fatal("untrusted request succeeded")
	}
	if request(true, false) != 201 {
		t.Fatal("pending account not activated")
	}
	if err := s.finishAdminSetup("operator", token, "different synthetic password"); err == nil {
		t.Fatal("old setup token remained valid")
	}
	v, ok := s.authenticateUser("operator", "synthetic long password")
	if !ok || !s.adminSessionValid("operator", v) {
		t.Fatal("administrator cannot authenticate")
	}
	if request(true, false) != 409 {
		t.Fatal("active account overwritten")
	}
}

func TestCreateSuperuserRejectsNonInteractiveTerminal(t *testing.T) {
	old := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = r
	defer func() { os.Stdin = old; r.Close(); w.Close() }()
	if err := createSuperuserCommand([]string{"--username", "fixture"}); err == nil {
		t.Fatal("non-interactive use accepted")
	}
}

func TestPasswordMinimumTenCharacters(t *testing.T) {
	if passwordValid("123456789") || !passwordValid("1234567890") || !passwordValid("абвгдежзий") {
		t.Fatal("incorrect minimum password length")
	}
	s := testStore(t)
	if err := s.createSuperuser("short", "123456789"); err != ErrInvalid {
		t.Fatal("nine-character password accepted")
	}
	if err := s.createSuperuser("valid", "1234567890"); err != nil {
		t.Fatal("ten-character password refused", err)
	}
	if _, ok := s.authenticateUser("valid", "1234567890"); !ok {
		t.Fatal("ten-character password cannot authenticate")
	}
}
