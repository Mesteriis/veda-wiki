package httpapi

import (
	"encoding/json"
	"fmt"
	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/importer"
	"github.com/Mesteriis/veda-wiki/internal/store"
	"net/http"
	"testing"
)

func TestImportLargeBoundedBatchIntoSQLite(t *testing.T) {
	s := testStore(t)
	source(t, s, "existing", "private")
	if err := store.MigrateSQLite(s.Dir()); err != nil {
		t.Fatal(err)
	}
	s, err := openStore(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	batch := testBatch()
	image := make([]byte, (4<<20)-1024)
	copy(image, []byte("\x89PNG\r\n\x1a\n"))
	batch.Assets = map[string][]byte{}
	for i := 0; i < 8; i++ {
		batch.Assets[fmt.Sprintf("assets/image-%d.png", i)] = image
	}
	encoded, err := json.Marshal(batch)
	if err != nil || len(encoded) <= 40<<20 || len(encoded) >= importer.MaxRequestBytes {
		t.Fatal("fixture did not cross previous transport limit", err)
	}
	out := request(Wiki{Store: s, Token: "test-owner"}, "POST", "/api/v1/import", string(encoded), true)
	if out.Code != http.StatusOK {
		t.Fatal("bounded large import failed", out.Code)
	}
	reopened, err := openStore(s.Dir())
	if err != nil || len(reopened.state(t).Sources["handbook"].Assets) != 8 || reopened.state(t).Articles["handbook/README.md"].Versions[0].Text != "# Initial" {
		t.Fatal("large import did not survive SQLite reopen", err)
	}
	batch.SourceID = "too-large"
	batch.Assets["assets/extra.png"] = image
	if _, err := reopened.importBatch(batch); err != domain.ErrInvalid {
		t.Fatal("import above aggregate limit was accepted", err)
	}
	if _, exists := reopened.state(t).Sources["too-large"]; exists {
		t.Fatal("rejected import created a source")
	}
}

func TestStreamingImportRejectsAmbiguousOrMalformedJSON(t *testing.T) {
	s := testStore(t)
	w := Wiki{Store: s, Token: "test-owner"}
	for _, body := range []string{
		`{"source_id":"one","source_id":"two"}`,
		`{"files":{"a.md":"one","a.md":"two"}}`,
		`{"assets":{"a.png":"a","a.png":"b"}}`,
		`{"unknown":true}`,
		`{"files":[]}`,
		`{"files":{"a.md":"text"}} {}`,
		`{"metadata":{"unknown":true}}`,
		`{"files":{"a.md":"text"}`,
	} {
		if got := request(w, "POST", "/api/v1/import", body, true); got.Code != http.StatusBadRequest {
			t.Fatal("malformed or ambiguous import accepted", got.Code)
		}
	}
}
