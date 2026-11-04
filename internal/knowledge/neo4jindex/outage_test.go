package neo4jindex

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/knowledge"
	"github.com/Mesteriis/veda-wiki/internal/knowledge/indextest"
)

// hungServer accepts connections and never answers, like a stuck Neo4j.
func hungServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var conns []net.Conn
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
	})
	return "bolt://" + ln.Addr().String()
}

// During an outage concurrent reads do not queue behind one another: each
// returns ErrUnavailable within its own deadline, whether the schema is
// still to be created or already exists.
func TestConcurrentReadsRespectDeadlinesWhenServerHangs(t *testing.T) {
	for _, ready := range []bool{false, true} {
		ix, err := New(Config{URI: hungServer(t), User: "neo4j", Password: "not-a-real-password", ConnectTimeout: 30 * time.Second, QueryTimeout: 30 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		ix.schemaReady = ready
		allowed := indextest.Owner(indextest.Database(indextest.PubA))
		const n, deadline = 16, 300 * time.Millisecond
		errs := make(chan error, n)
		start := time.Now()
		for range n {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), deadline)
				defer cancel()
				_, err := ix.Entities(ctx, allowed, indextest.FP, knowledge.EntityQuery{})
				errs <- err
			}()
		}
		for range n {
			if err := <-errs; !errors.Is(err, ErrUnavailable) {
				t.Fatalf("ready=%v: %v", ready, err)
			}
		}
		if elapsed := time.Since(start); elapsed > deadline+700*time.Millisecond {
			t.Fatalf("ready=%v: reads took %v with a %v deadline", ready, elapsed, deadline)
		}
		// While the attempt is still pending, a caller without a deadline
		// waits for it at most schemaWait.
		if !ready {
			start = time.Now()
			if err := ix.Status(context.Background()); !errors.Is(err, ErrUnavailable) || time.Since(start) > schemaWait+500*time.Millisecond {
				t.Fatalf("no fail-fast while the schema attempt hangs: %v %v", err, time.Since(start))
			}
		}
		closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = ix.Close(closeCtx)
		cancel()
	}
}
