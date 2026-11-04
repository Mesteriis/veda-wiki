package httpapi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This fixture is explicitly invoked by the disposable browser smoke test.
func TestCreateBrowserAdminFixture(t *testing.T) {
	dir := os.Getenv("VEDA_UI_ADMIN_FIXTURE_DIR")
	if dir == "" {
		t.Skip("browser fixture not requested")
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil || filepath.Base(resolved) != "data" || !strings.HasPrefix(filepath.Base(filepath.Dir(resolved)), "veda-ui-fixture-") {
		t.Fatal("browser fixture must use its temporary data directory")
	}
	s, err := openStore(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.auth().CreateSuperuser("browser-admin", "synthetic admin password"); err != nil {
		t.Fatal(err)
	}
}
