package auth

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store/storetest"
)

// blockingKey replaces the Argon2id derivation with one that reports each
// entry on entered and waits for release. It restores the real derivation
// when the test ends.
func blockingKey(t *testing.T) (entered chan struct{}, release chan struct{}, calls *atomic.Int32) {
	t.Helper()
	entered = make(chan struct{}, 16)
	release = make(chan struct{})
	calls = &atomic.Int32{}
	original := passwordKey
	passwordKey = func(password, salt []byte) []byte {
		calls.Add(1)
		entered <- struct{}{}
		<-release
		return make([]byte, 32)
	}
	t.Cleanup(func() { passwordKey = original })
	return entered, release, calls
}

func waitEntered(t *testing.T, entered chan struct{}, n int) {
	t.Helper()
	for range n {
		select {
		case <-entered:
		case <-time.After(10 * time.Second):
			t.Fatal("derivation did not start")
		}
	}
}

func TestTwoPasswordDerivationsRunConcurrentlyAndThirdWaits(t *testing.T) {
	entered, release, calls := blockingKey(t)
	var wg sync.WaitGroup
	for range passwordWorkers {
		wg.Go(func() {
			if _, err := derive(context.Background(), "synthetic password", make([]byte, 16)); err != nil {
				t.Error(err)
			}
		})
	}
	// Both derivations are inside the key function at the same time.
	waitEntered(t, entered, passwordWorkers)

	third := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_, err := derive(ctx, "synthetic password", make([]byte, 16))
		third <- err
	}()
	// A third derivation holds no slot: cancelling it ends it before it
	// reaches the key function, while both slots are still occupied.
	cancel()
	select {
	case err := <-third:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("cancelled waiter returned", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancelled waiter did not return")
	}
	if got := calls.Load(); got != passwordWorkers {
		t.Fatalf("key function called %d times, want %d", got, passwordWorkers)
	}
	close(release)
	wg.Wait()
}

func TestWaitingPasswordDerivationStartsWhenSlotFrees(t *testing.T) {
	entered, release, calls := blockingKey(t)
	var wg sync.WaitGroup
	for range passwordWorkers + 1 {
		wg.Go(func() {
			if _, err := derive(context.Background(), "synthetic password", make([]byte, 16)); err != nil {
				t.Error(err)
			}
		})
	}
	waitEntered(t, entered, passwordWorkers)
	close(release)
	waitEntered(t, entered, 1)
	wg.Wait()
	if got := calls.Load(); got != passwordWorkers+1 {
		t.Fatalf("key function called %d times, want %d", got, passwordWorkers+1)
	}
}

func TestCancelledPasswordChecksFailWithoutDeriving(t *testing.T) {
	s := Service{Store: storetest.New(t)}
	if err := s.CreateSuperuser("admin", "synthetic password"); err != nil {
		t.Fatal(err)
	}
	pending := Service{Store: storetest.New(t)}
	token, err := pending.BootstrapAdmin("operator")
	if err != nil {
		t.Fatal(err)
	}
	_, _, calls := blockingKey(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok := s.AuthenticateUserContext(ctx, "admin", "synthetic password"); ok {
		t.Fatal("cancelled login authenticated")
	}
	if _, ok := s.AuthenticateUserContext(ctx, "unknown", "synthetic password"); ok {
		t.Fatal("cancelled unknown login authenticated")
	}
	if err := pending.FinishAdminSetupContext(ctx, "operator", token, "another synthetic password"); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("cancelled setup returned", err)
	}
	if calls.Load() != 0 {
		t.Fatal("cancelled check derived a key")
	}
}

// Logins authenticate outside the session lock, so two password checks can
// use both password workers and cookie checks are not held up meanwhile.
func TestLoginsAuthenticateOutsideSessionLock(t *testing.T) {
	s := &Sessions{}
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var wg sync.WaitGroup
	for _, identity := range []string{"first", "second"} {
		wg.Go(func() {
			_, err := s.Issue(Login{Now: time.Now(), Limited: true, Kind: "user", Identity: identity, Authenticate: func() (Grant, bool) {
				entered <- struct{}{}
				<-release
				return Grant{}, true
			}})
			if err != nil {
				t.Error(err)
			}
		})
	}
	waitEntered(t, entered, 2)
	if active, _ := s.Count(); active != 0 {
		t.Fatal("session issued before authentication finished")
	}
	close(release)
	wg.Wait()
	if active, _ := s.Count(); active != 2 {
		t.Fatalf("issued %d sessions, want 2", active)
	}
}
