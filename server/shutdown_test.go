package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestShutdownWaitsForRequestAndWorker(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		_, _ = io.WriteString(w, "finished")
	})}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	workerRelease := make(chan struct{})
	var workers sync.WaitGroup
	workers.Add(1)
	go func() { defer workers.Done(); <-ctx.Done(); <-workerRelease }()
	done := make(chan error, 1)
	go func() {
		done <- serveUntilStopped(ctx, stop, []servedListener{{server: server, listener: listener}}, &workers)
	}()
	response := make(chan error, 1)
	go func() {
		r, e := http.Get("http://" + listener.Addr().String())
		if e == nil {
			_, e = io.ReadAll(r.Body)
			_ = r.Body.Close()
		}
		response <- e
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not reach handler")
	}
	stop()
	select {
	case <-done:
		t.Fatal("shutdown returned before request and worker finished")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if e := <-response; e != nil {
		t.Fatal(e)
	}
	select {
	case <-done:
		t.Fatal("shutdown returned before worker finished")
	case <-time.After(50 * time.Millisecond):
	}
	close(workerRelease)
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not finish")
	}
}

func TestListenerFailureStopsOtherListenersAndWorkers(t *testing.T) {
	broken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err = broken.Close(); err != nil {
		t.Fatal(err)
	}
	other, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	var workers sync.WaitGroup
	workers.Add(1)
	go func() { defer workers.Done(); <-ctx.Done() }()
	done := make(chan error, 1)
	go func() {
		done <- serveUntilStopped(ctx, stop, []servedListener{{server: &http.Server{}, listener: broken}, {server: &http.Server{}, listener: other}}, &workers)
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("listener failure was ignored")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("listener failure did not stop service")
	}
	conn, err := net.DialTimeout("tcp", other.Addr().String(), time.Second)
	if err == nil {
		conn.Close()
		t.Fatal("other listener remained open")
	}
}
