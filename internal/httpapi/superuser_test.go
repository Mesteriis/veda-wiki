package httpapi

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCreateSuperuserRestrictedAndActivatesPending(t *testing.T) {
	s := testStore(t)
	token, err := s.auth().BootstrapAdmin("operator")
	if err != nil {
		t.Fatal(err)
	}
	w := Wiki{Store: s, ReaderLocal: true, LocalImportSource: "infra"}
	body := `{"id":"operator","password":"synthetic long password"}`
	request := func(root bool, origin bool) int {
		r := httptest.NewRequest("POST", "http://localhost/api/local/createsuperuser", strings.NewReader(body))
		if root {
			r = r.WithContext(context.WithValue(r.Context(), LocalRootKey{}, true))
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
	if err := s.auth().FinishAdminSetup("operator", token, "different synthetic password"); err == nil {
		t.Fatal("old setup token remained valid")
	}
	v, ok := s.auth().AuthenticateUser("operator", "synthetic long password")
	if !ok || !s.auth().AdminSessionValid("operator", v) {
		t.Fatal("administrator cannot authenticate")
	}
	if request(true, false) != 409 {
		t.Fatal("active account overwritten")
	}
}
