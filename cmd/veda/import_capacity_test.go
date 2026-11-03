package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestImportLargeBoundedBatchIntoSQLite(t *testing.T) {
	s := testStore(t)
	source(t, s, "existing", "private")
	if err := migrateSQLite(s.dir); err != nil {
		t.Fatal(err)
	}
	s, err := openStore(s.dir)
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
	if err != nil || len(encoded) <= 40<<20 || len(encoded) >= maxImportRequestBytes {
		t.Fatal("fixture did not cross previous transport limit", err)
	}
	out := request(Wiki{Store: s, Token: "test-owner"}, "POST", "/api/v1/import", string(encoded), true)
	if out.Code != http.StatusOK {
		t.Fatal("bounded large import failed", out.Code)
	}
	reopened, err := openStore(s.dir)
	if err != nil || len(reopened.db.Sources["handbook"].Assets) != 8 || reopened.db.Articles["handbook/README.md"].Versions[0].Text != "# Initial" {
		t.Fatal("large import did not survive SQLite reopen", err)
	}
	batch.SourceID = "too-large"
	batch.Assets["assets/extra.png"] = image
	if _, err := reopened.importBatch(batch); err != ErrInvalid {
		t.Fatal("import above aggregate limit was accepted", err)
	}
	if _, exists := reopened.db.Sources["too-large"]; exists {
		t.Fatal("rejected import created a source")
	}
}

func TestCheckoutPreviewAcceptsMoreThanFormerLimit(t *testing.T) {
	files := map[string]string{"veda.yaml": metadataFixture}
	text := strings.Repeat("a", 900<<10)
	for i := 0; i < 20; i++ {
		files[fmt.Sprintf("docs/page-%02d.md", i)] = text
	}
	checkout := gitFixture(t, files)
	batch, err := prepareCheckout(checkout, "fixture")
	if err != nil || batch.Kind != "wiki" || len(batch.Files) != 20 || !batch.Complete {
		t.Fatal("bounded large checkout was not imported", err)
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
