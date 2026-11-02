package main

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// The child runs only in an isolated test subprocess with a synthetic store and
// loopback fake GitHub endpoint; it is not a production server entry point.
func TestSyncRecoveryFixtureProcess(t *testing.T) {
	if os.Getenv("VEDA_RECOVERY_FIXTURE") != "1" {
		t.Skip("fixture subprocess only")
	}
	dir, endpoint := os.Getenv("VEDA_RECOVERY_DIR"), os.Getenv("VEDA_RECOVERY_ENDPOINT")
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() || !filepath.IsAbs(dir) {
		t.Fatal("invalid fixture parameters")
	}
	s, err := openStore(dir)
	if err != nil {
		t.Fatal("fixture reopen failed", err)
	}
	if err = s.retryConnection("handbook"); err != nil {
		t.Fatal("fixture retry failed", err)
	}
	client := &GitHubClient{base: endpoint, http: &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}}
	m := newSyncManager(s, client)
	for i := 0; i < 4; i++ {
		m.Once(context.Background())
		jobs := s.jobs()
		if len(jobs) == 1 && jobs[0].State == "complete" {
			return
		}
	}
	t.Fatal("fixture job was not acknowledged")
}

func TestSQLitePreparedWriteRecoversInFreshProcess(t *testing.T) {
	s := testStore(t)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Original"})
	m := connectFixture(t, s, f, true)
	if _, err := s.put("handbook", "README.md", "# Synthetic durable change", 1); err != nil {
		t.Fatal(err)
	}
	f.modify(func() { f.loseResponse = true })
	m.Once(context.Background())
	jobs := s.jobs()
	if f.writeCount() != 1 || len(jobs) != 1 || jobs[0].PreparedCommit == "" {
		t.Fatal("fixture did not preserve ambiguous commit")
	}
	if err := migrateSQLite(s.dir); err != nil {
		t.Fatal("fixture migration failed", err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestSyncRecoveryFixtureProcess$")
	cmd.Env = append(os.Environ(), "VEDA_RECOVERY_FIXTURE=1", "VEDA_RECOVERY_DIR="+s.dir, "VEDA_RECOVERY_ENDPOINT="+f.server.URL)
	if err := cmd.Run(); err != nil {
		t.Fatal("recovery subprocess failed", err)
	}
	reopened, err := openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	jobs = reopened.jobs()
	if len(jobs) != 1 || jobs[0].State != "complete" || f.writeCount() != 1 || f.contents()["README.md"] != "# Synthetic durable change" {
		t.Fatal("recovery lost the write or duplicated the remote commit")
	}
}

func TestSQLiteIndexDirectoryMoveRecoversInFreshProcess(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	s.put("from", "guide.md", "# Guide\n\n[Docs](docs/)", 0)
	s.put("from", "docs/index.md", "# Synthetic index", 0)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target"})
	m := connectFixture(t, s, f, true)
	move, err := s.moveArticle(moveRequest{SourceID: "from", SourcePath: "guide.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "guide.md", IncludeLinked: true, ConfirmMove: true, ConfirmPublication: true})
	if err != nil || move.State != "waiting_destination" || s.db.Version != 17 {
		t.Fatal("fixture move not queued", err)
	}
	f.modify(func() { f.loseResponse = true })
	m.Once(context.Background())
	jobs := s.jobs()
	if f.writeCount() != 1 || len(jobs) != 1 || jobs[0].PreparedCommit == "" || s.db.Articles["from/guide.md"].Deleted {
		t.Fatal("ambiguous move was not retained")
	}
	if err = migrateSQLite(s.dir); err != nil {
		t.Fatal("fixture migration failed", err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestSyncRecoveryFixtureProcess$")
	cmd.Env = append(os.Environ(), "VEDA_RECOVERY_FIXTURE=1", "VEDA_RECOVERY_DIR="+s.dir, "VEDA_RECOVERY_ENDPOINT="+f.server.URL)
	if err = cmd.Run(); err != nil {
		t.Fatal("recovery subprocess failed", err)
	}
	reopened, err := openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	jobs = reopened.jobs()
	if len(jobs) != 1 || jobs[0].State != "complete" || f.writeCount() != 1 || f.contents()["docs/README.md"] != "# Synthetic index" || reopened.moves()[0].State != "complete" || !reopened.db.Articles["from/guide.md"].Deleted || reopened.db.Articles["from/docs/index.md"].Deleted {
		t.Fatal("recovery lost or duplicated the mapped move")
	}
}
