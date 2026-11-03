package app

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func runWithServe(t *testing.T, fake func() error) (dataDir string, runErr error) {
	t.Helper()
	previous := serve
	serve = func(ctx context.Context, stop context.CancelFunc, _ []servedListener, workers *sync.WaitGroup) error {
		err := fake()
		stop()
		workers.Wait()
		return err
	}
	t.Cleanup(func() { serve = previous; abandonedWriterLock = nil })
	dataDir = filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dataDir, 0700); err != nil {
		t.Fatal(err)
	}
	runErr = Run(context.Background(), []string{"-data", dataDir, "-listen", "127.0.0.1:0", "-public-listen", "127.0.0.1:0"})
	return dataDir, runErr
}

// Run must keep the writer lock after a drain timeout, because a worker or
// request may still commit, and release it after any other outcome.
func TestRunKeepsWriterLockAfterDrainTimeout(t *testing.T) {
	dataDir, err := runWithServe(t, func() error { return errShutdownTimeout })
	if err == nil {
		t.Fatal("drain timeout not returned by Run")
	}
	if abandonedWriterLock == nil || lockable(t, filepath.Join(dataDir, "writer.lock")) {
		t.Fatal("Run released the writer lock after a drain timeout")
	}
}

func TestRunReleasesWriterLockAfterCleanStop(t *testing.T) {
	dataDir, err := runWithServe(t, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if abandonedWriterLock != nil || !lockable(t, filepath.Join(dataDir, "writer.lock")) {
		t.Fatal("Run kept the writer lock after a clean stop")
	}
}
