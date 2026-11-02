package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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
		complete := len(jobs) > 0
		for _, job := range jobs {
			if job.State != "complete" {
				complete = false
			}
		}
		if complete {
			return
		}
	}
	t.Fatal("fixture job was not acknowledged")
}

func TestSQLiteCrashBeforeRemoteCommitPreservesLaterEdit(t *testing.T) {
	s := testStore(t)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Original"})
	connectFixture(t, s, f, true)
	if err := migrateSQLite(s.dir); err != nil {
		t.Fatal(err)
	}
	var err error
	s, err = openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("handbook", "README.md", "# First edit", 1); err != nil {
		t.Fatal(err)
	}
	arrived, released := make(chan struct{}), make(chan struct{})
	var intercepted atomic.Bool
	var releaseOnce sync.Once
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/graphql" {
			body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if err != nil {
				http.Error(w, "fixture read failed", 500)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			if bytes.Contains(body, []byte("updateRefs")) && intercepted.CompareAndSwap(false, true) {
				close(arrived)
				// This fixture drops the first request before applying it. A real
				// server might instead finish a request after disconnection.
				<-released
				return
			}
		}
		f.serve(w, r)
	}))
	defer gateway.Close()
	defer releaseOnce.Do(func() { close(released) })
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, exe, "-test.run=^TestSyncRecoveryFixtureProcess$")
	child.Env = append(os.Environ(), "VEDA_RECOVERY_FIXTURE=1", "VEDA_RECOVERY_DIR="+s.dir, "VEDA_RECOVERY_ENDPOINT="+gateway.URL)
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if child.ProcessState == nil {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	}()
	select {
	case <-arrived:
	case <-ctx.Done():
		t.Fatal("fixture ref update was not reached")
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err == nil {
		t.Fatal("fixture process unexpectedly exited normally")
	}
	releaseOnce.Do(func() { close(released) })
	s, err = openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	jobs := s.jobs()
	if len(jobs) != 1 || jobs[0].State != "prepared" || jobs[0].PreparedCommit == "" || f.writeCount() != 0 {
		t.Fatal("prepared state or remote non-write was not preserved", jobs)
	}
	if _, err := s.put("handbook", "README.md", "# Second edit", 2); err != nil {
		t.Fatal(err)
	}
	recovery := exec.CommandContext(ctx, exe, "-test.run=^TestSyncRecoveryFixtureProcess$")
	recovery.Env = append(os.Environ(), "VEDA_RECOVERY_FIXTURE=1", "VEDA_RECOVERY_DIR="+s.dir, "VEDA_RECOVERY_ENDPOINT="+gateway.URL)
	if err := recovery.Run(); err != nil {
		t.Fatal("recovery process failed", err)
	}
	s, err = openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	jobs = s.jobs()
	if len(jobs) != 2 || jobs[0].State != "complete" || jobs[1].State != "complete" || f.writeCount() != 2 || f.contents()["README.md"] != "# Second edit" {
		t.Fatal("crash recovery lost or duplicated a revision", jobs, f.writeCount())
	}
}

func TestSQLiteCrashAfterRemoteCommitPreservesLaterEdit(t *testing.T) {
	s := testStore(t)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Original"})
	connectFixture(t, s, f, true)
	if err := migrateSQLite(s.dir); err != nil {
		t.Fatal(err)
	}
	var err error
	s, err = openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("handbook", "README.md", "# First edit", 1); err != nil {
		t.Fatal(err)
	}

	committed, release := make(chan struct{}), make(chan struct{})
	var notifyOnce, releaseOnce sync.Once
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := httptest.NewRecorder()
		f.serve(response, r)
		if r.URL.Path == "/graphql" && strings.Contains(response.Body.String(), `"updateRefs"`) {
			notifyOnce.Do(func() { close(committed); <-release })
		}
		for key, values := range response.Header() {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(response.Code)
		_, _ = w.Write(response.Body.Bytes())
	}))
	defer gateway.Close()
	defer releaseOnce.Do(func() { close(release) })
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, exe, "-test.run=^TestSyncRecoveryFixtureProcess$")
	child.Env = append(os.Environ(), "VEDA_RECOVERY_FIXTURE=1", "VEDA_RECOVERY_DIR="+s.dir, "VEDA_RECOVERY_ENDPOINT="+gateway.URL)
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if child.ProcessState == nil {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	}()
	select {
	case <-committed:
	case <-ctx.Done():
		t.Fatal("fixture remote update was not reached")
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err == nil {
		t.Fatal("fixture process unexpectedly exited normally")
	}
	releaseOnce.Do(func() { close(release) })
	s, err = openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	jobs := s.jobs()
	if len(jobs) != 1 || jobs[0].State != "prepared" || jobs[0].PreparedCommit == "" || f.writeCount() != 1 {
		t.Fatal("prepared state was not durable across termination", jobs)
	}
	if _, err := s.put("handbook", "README.md", "# Second edit", 2); err != nil {
		t.Fatal(err)
	}
	recovery := exec.CommandContext(ctx, exe, "-test.run=^TestSyncRecoveryFixtureProcess$")
	recovery.Env = append(os.Environ(), "VEDA_RECOVERY_FIXTURE=1", "VEDA_RECOVERY_DIR="+s.dir, "VEDA_RECOVERY_ENDPOINT="+gateway.URL)
	if err := recovery.Run(); err != nil {
		t.Fatal("recovery process failed", err)
	}
	s, err = openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	jobs = s.jobs()
	if len(jobs) != 2 || jobs[0].State != "complete" || jobs[1].State != "complete" || f.writeCount() != 2 || f.contents()["README.md"] != "# Second edit" {
		t.Fatal("crash recovery lost or duplicated a revision", jobs, f.writeCount())
	}
}

func TestSQLiteLaterEditAfterLostResponseRecoversInFreshProcess(t *testing.T) {
	s := testStore(t)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Original"})
	connectFixture(t, s, f, true)
	if err := migrateSQLite(s.dir); err != nil {
		t.Fatal(err)
	}
	var err error
	s, err = openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.put("handbook", "README.md", "# First edit", 1); err != nil {
		t.Fatal(err)
	}
	f.modify(func() { f.loseResponse = true })
	newSyncManager(s, f.client()).Once(context.Background())
	if jobs := s.jobs(); len(jobs) != 1 || jobs[0].PreparedCommit == "" || f.writeCount() != 1 {
		t.Fatal("fixture did not retain uncertain write", jobs)
	}
	if _, err = s.put("handbook", "README.md", "# Second edit", 2); err != nil {
		t.Fatal(err)
	}
	if jobs := s.jobs(); len(jobs) != 2 || jobs[0].State != "retry" || jobs[1].State != "pending" {
		t.Fatal("later edit did not preserve both jobs", jobs)
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
	jobs := reopened.jobs()
	if len(jobs) != 2 || jobs[0].State != "complete" || jobs[1].State != "complete" || f.writeCount() != 2 || f.contents()["README.md"] != "# Second edit" {
		t.Fatal("fresh process lost or duplicated a revision", jobs, f.writeCount())
	}
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
