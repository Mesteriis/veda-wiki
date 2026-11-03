package httpapi

import (
	"bytes"
	"context"
	"github.com/Mesteriis/veda-wiki/internal/github"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
	"os"
	"path/filepath"
	"testing"
)

func TestGitHubUnchangedHeadRepairsCorruptAsset(t *testing.T) {
	s := testStore(t)
	image := smallPNG(t)
	f := githubtest.New(t, map[string]string{
		"veda.yaml": metadataFixture, "guide.md": "# Guide\n\n![image](image.png)", "image.png": string(image),
	})
	f.Modify(func() { f.Private = true })
	m := connectFixture(t, s, f, false)
	ref := s.DB().Sources["handbook"].Assets["image.png"]
	if ref.SHA == "" {
		t.Fatal("fixture image was not imported")
	}
	filename := filepath.Join(s.Dir(), "blobs", ref.SHA)
	if err := os.WriteFile(filename, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	// Remove the independent Git object cache to exercise the upstream blob path.
	gitSHA := github.BlobDigest(image, 40)
	if err := os.Remove(filepath.Join(s.Dir(), "git-objects", "41", gitSHA)); err != nil {
		t.Fatal(err)
	}
	before := s.DB().Sources["handbook"].ImportGeneration
	m.Once(context.Background())
	if err := s.verifyAsset(ref); err != nil {
		t.Fatal("unchanged head did not repair asset", err)
	}
	if s.DB().Sources["handbook"].ImportGeneration != before {
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
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "# Guide", "image.png": string(image)})
	f.Modify(func() { f.Private = true })
	m := connectFixture(t, s, f, false)
	before := observedAt(s, "handbook")
	ref := s.DB().Sources["handbook"].Assets["image.png"]
	if err := os.Remove(filepath.Join(s.Dir(), "blobs", ref.SHA)); err != nil {
		t.Fatal(err)
	}
	gitSHA := github.BlobDigest(image, 40)
	if err := os.Remove(filepath.Join(s.Dir(), "git-objects", "41", gitSHA)); err != nil {
		t.Fatal(err)
	}
	f.Modify(func() { delete(f.Blobs, gitSHA) })
	m.Once(context.Background())
	if !observedAt(s, "handbook").Equal(before) {
		t.Fatal("failed repair advanced observation")
	}
	if conn := s.connections()[0]; conn.State != "error" {
		t.Fatal("failed repair was not reported", conn.State)
	}
}

func TestGitHubPartialSnapshotReportsUnrecoverableRetainedAsset(t *testing.T) {
	s := testStore(t)
	image := smallPNG(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "# Guide", "image.png": string(image)})
	f.Modify(func() { f.Private = true })
	m := connectFixture(t, s, f, false)
	ref := s.DB().Sources["handbook"].Assets["image.png"]
	f.Advance(map[string]string{"veda.yaml": metadataFixture, "guide.md": "# Guide", "bad.md": string([]byte{0xff})})
	syncFixtureSource(t, m, "handbook")
	if source := s.DB().Sources["handbook"]; !source.ImportPartial || source.Assets["image.png"] != ref {
		t.Fatal("fixture did not retain partial asset")
	}
	if err := os.Remove(filepath.Join(s.Dir(), "blobs", ref.SHA)); err != nil {
		t.Fatal(err)
	}
	syncFixtureSource(t, m, "handbook")
	if conn := s.connections()[0]; conn.State != "error" || conn.LastError != "asset_unavailable" {
		t.Fatal("unrecoverable retained asset not reported", conn.State, conn.LastError)
	}
	ctx, err := s.agentContext("handbook", "guide.md", false)
	if err != nil || ctx.SourceStatus.SyncIssue != "error" || ctx.SourceStatus.SyncError != "asset_unavailable" || !ctx.SourceStatus.ImportPartial {
		t.Fatal("agent did not receive source failure", err)
	}
}
