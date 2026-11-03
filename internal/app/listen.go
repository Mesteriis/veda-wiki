package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

type servedListener struct {
	server    *http.Server
	listener  net.Listener
	cert, key string
}

// Drain every listener and wait for background workers before releasing the
// store lock. A bounded timeout prevents an unresponsive client from hanging shutdown.
func serveUntilStopped(ctx context.Context, stop context.CancelFunc, endpoints []servedListener, workers *sync.WaitGroup) error {
	errs := make(chan error, len(endpoints))
	var serving sync.WaitGroup
	for _, endpoint := range endpoints {
		serving.Add(1)
		go func(e servedListener) {
			defer serving.Done()
			if e.cert != "" {
				errs <- e.server.ServeTLS(e.listener, e.cert, e.key)
			} else {
				errs <- e.server.Serve(e.listener)
			}
		}(endpoint)
	}
	var first error
	select {
	case <-ctx.Done():
	case first = <-errs:
	}
	stop()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var drains sync.WaitGroup
	shutdownErrors := make(chan error, len(endpoints))
	for _, endpoint := range endpoints {
		drains.Add(1)
		go func(s *http.Server) {
			defer drains.Done()
			if err := s.Shutdown(shutdownCtx); err != nil {
				shutdownErrors <- err
				_ = s.Close()
			}
		}(endpoint.server)
	}
	done := make(chan struct{})
	go func() { drains.Wait(); serving.Wait(); workers.Wait(); close(done) }()
	select {
	case <-done:
	case <-shutdownCtx.Done():
		return errors.Join(first, errors.New("shutdown timed out before requests and workers finished"))
	}
	if errors.Is(first, http.ErrServerClosed) {
		first = nil
	}
	close(shutdownErrors)
	for err := range shutdownErrors {
		first = errors.Join(first, err)
	}
	return first
}

// loadOrCreateKey never logs key material; both files are local private state.
func loadOrCreateKey(dir, name string) ([]byte, error) {
	keyPath := filepath.Join(dir, name)
	if info, e := os.Lstat(keyPath); e == nil && (!info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0) {
		return nil, domain.ErrInvalid
	}
	key, err := os.ReadFile(keyPath)
	if os.IsNotExist(err) {
		secret := make([]byte, 32)
		if _, err = rand.Read(secret); err != nil {
			return nil, err
		}
		f, e := os.OpenFile(keyPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return nil, e
		}
		_, err = f.WriteString(hex.EncodeToString(secret))
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
		key, err = os.ReadFile(keyPath)
	}
	if err != nil || len(key) != 64 {
		return nil, domain.ErrInvalid
	}
	if _, err = hex.DecodeString(string(key)); err != nil {
		return nil, domain.ErrInvalid
	}
	return key, nil
}

// The parent directory must belong to the service user and be inaccessible to
// others. Never remove an existing path, which could belong to another process.
func listenReaderSocket(name string) (net.Listener, error) {
	if !filepath.IsAbs(name) || filepath.Clean(name) != name {
		return nil, domain.ErrInvalid
	}
	parent, err := os.Lstat(filepath.Dir(name))
	if err != nil || !parent.IsDir() || parent.Mode().Perm()&0077 != 0 {
		return nil, domain.ErrInvalid
	}
	stat, ok := parent.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Getuid() {
		return nil, domain.ErrInvalid
	}
	if _, err = os.Lstat(name); err == nil || !os.IsNotExist(err) {
		return nil, domain.ErrInvalid
	}
	listener, err := net.Listen("unix", name)
	if err != nil {
		return nil, err
	}
	if err = os.Chmod(name, 0600); err != nil {
		listener.Close()
		return nil, err
	}
	return listener, nil
}
