package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestReaderTranslationValidationIsScopedAndNonMutating(t *testing.T) {
	s := testStore(t)
	for _, id := range []string{"alpha", "beta"} {
		if err := s.addSource(Source{ID: id, Title: id, Kind: "program", Visibility: "private"}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.put(id, "docs/guide.md", "# Guide\n\nRun `install`.", 0); err != nil {
			t.Fatal(err)
		}
	}
	key, err := s.createAccessKey("reader", []string{"alpha"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	rev := s.db.Articles["alpha/docs/guide.md"].Versions[0]
	in := translationRequest{SourceID: "alpha", Path: "docs/guide.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Руководство\n\nВыполните `install`."}
	before, err := json.Marshal(s.db)
	if err != nil {
		t.Fatal(err)
	}
	w := Wiki{Store: s, Token: "owner", ReaderToken: "reader"}
	call := func(input translationRequest, token, origin string, local, public bool) *httptest.ResponseRecorder {
		body, _ := json.Marshal(input)
		r := httptest.NewRequest("POST", "/api/v1/articles/translation-validate", bytes.NewReader(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		target := w
		target.ReaderLocal = local
		target.Public = public
		out := httptest.NewRecorder()
		target.ServeHTTP(out, r)
		return out
	}
	r := call(in, key, "", false, false)
	var result map[string]any
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &result) != nil || result["structural_valid"] != true || result["russian_prose"] != true || result["saved"] != false {
		t.Fatal("valid proposal rejected", r.Code)
	}
	broken := in
	broken.Text = "# Руководство\n\nВыполните `other`."
	r = call(broken, key, "", false, false)
	result = nil
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &result) != nil || result["structural_valid"] != false {
		t.Fatal("changed code not detected")
	}
	other := in
	other.SourceID = "beta"
	if call(other, key, "", false, false).Code != 404 {
		t.Fatal("scope escaped")
	}
	stale := in
	stale.ExpectedRevision = 2
	if call(stale, key, "", false, false).Code != 409 {
		t.Fatal("stale revision accepted")
	}
	if call(in, key, "http://localhost", false, false).Code != 403 || call(in, key, "", false, true).Code != 403 {
		t.Fatal("browser or public access accepted")
	}
	if call(in, "", "", true, false).Code != 200 {
		t.Fatal("local read-only socket denied validation")
	}
	var wg sync.WaitGroup
	failures := make(chan int, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if code := call(in, key, "", false, false).Code; code != 200 {
				failures <- code
			}
		}()
	}
	wg.Wait()
	close(failures)
	if len(failures) != 0 {
		t.Fatal("concurrent validation failed")
	}
	after, _ := json.Marshal(s.db)
	if !bytes.Equal(after, before) {
		t.Fatal("validation mutated state")
	}
	if err := s.revokeAccessKey("reader"); err != nil {
		t.Fatal(err)
	}
	if call(in, key, "", false, false).Code != 403 {
		t.Fatal("revoked key accepted")
	}
}
