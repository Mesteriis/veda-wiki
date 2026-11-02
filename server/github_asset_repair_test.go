package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestGitHubUnchangedHeadRepairsCorruptAsset(t *testing.T) {
	s := testStore(t)
	image := smallPNG(t)
	f := newFakeGitHub(t, map[string]string{
		"veda.yaml": metadataFixture, "guide.md": "# Guide\n\n![image](image.png)", "image.png": string(image),
	})
	f.modify(func() { f.private = true })
	m := connectFixture(t, s, f, false)
	ref := s.db.Sources["handbook"].Assets["image.png"]
	if ref.SHA == "" {
		t.Fatal("fixture image was not imported")
	}
	filename := filepath.Join(s.dir, "blobs", ref.SHA)
	if err := os.WriteFile(filename, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	// Remove the independent Git object cache to exercise the upstream blob path.
	gitSHA := gitBlobDigest(image, 40)
	if err := os.Remove(filepath.Join(s.dir, "git-objects", "41", gitSHA)); err != nil {
		t.Fatal(err)
	}
	before := s.db.Sources["handbook"].ImportGeneration
	m.Once(context.Background())
	if err := s.verifyAsset(ref); err != nil {
		t.Fatal("unchanged head did not repair asset", err)
	}
	if s.db.Sources["handbook"].ImportGeneration != before {
		t.Fatal("unchanged head changed import generation")
	}
	media := request(Wiki{Store: s, Token: "test-owner"}, "GET", "/media/handbook/image.png", "", true)
	if media.Code != 200 || !bytes.Equal(media.Body.Bytes(), image) {
		t.Fatal("repaired image not served", media.Code)
	}
}

func TestGitHubAssetRepairFailureDoesNotObserveHead(t *testing.T) {
	s := testStore(t)
	image := smallPNG(t)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "# Guide", "image.png": string(image)})
	f.modify(func() { f.private = true })
	m := connectFixture(t, s, f, false)
	before := s.observed["handbook"]
	ref := s.db.Sources["handbook"].Assets["image.png"]
	if err := os.Remove(filepath.Join(s.dir, "blobs", ref.SHA)); err != nil {
		t.Fatal(err)
	}
	gitSHA := gitBlobDigest(image, 40)
	if err := os.Remove(filepath.Join(s.dir, "git-objects", "41", gitSHA)); err != nil {
		t.Fatal(err)
	}
	f.modify(func() { delete(f.blobs, gitSHA) })
	m.Once(context.Background())
	if !s.observed["handbook"].Equal(before) {
		t.Fatal("failed repair advanced observation")
	}
	if conn := s.connections()[0]; conn.State != "error" {
		t.Fatal("failed repair was not reported", conn.State)
	}
}
