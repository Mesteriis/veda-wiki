package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "github" {
		if e := githubCommand(os.Args[2:]); e != nil {
			log.Fatal(e)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "import" {
		if e := importCommand(os.Args[2:]); e != nil {
			log.Fatal(e)
		}
		return
	}
	data := flag.String("data", ".veda", "Private local data directory (single writer)")
	listen := flag.String("listen", "127.0.0.1:8080", "API and viewer address")
	public := flag.String("public-listen", "127.0.0.1:8081", "Public-only address")
	hosts := flag.String("allowed-hosts", "localhost,127.0.0.1,::1", "Allowed HTTP Host names")
	interval := flag.Duration("sync-interval", 5*time.Minute, "Background repository check interval (5s to 1h)")
	flag.Parse()
	if *interval < 5*time.Second || *interval > time.Hour {
		log.Fatal("invalid sync interval")
	}
	store, err := openStore(*data)
	if err != nil {
		log.Fatal(err)
	}
	lock, err := os.OpenFile(filepath.Join(*data, "writer.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		log.Fatal(err)
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		log.Fatal("data directory already in use")
	}
	// Reload under the process lock, avoiding startup races with another writer.
	store, err = openStore(*data)
	if err != nil {
		log.Fatal(err)
	}
	token, err := loadOrCreateKey(*data, "owner.key")
	if err != nil {
		log.Fatal("owner key unavailable or invalid")
	}
	readerToken, err := loadOrCreateKey(*data, "reader.key")
	if err != nil {
		log.Fatal("reader key unavailable or invalid")
	}
	hostList := strings.Split(*hosts, ",")
	for i := range hostList {
		hostList[i] = strings.TrimSpace(hostList[i])
		if hostList[i] == "" {
			log.Fatal("empty allowed host")
		}
	}
	apiListener, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatal(err)
	}
	publicListener, err := net.Listen("tcp", *public)
	if err != nil {
		apiListener.Close()
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	manager := newSyncManager(store, newGitHubClient())
	go manager.Run(ctx, *interval)
	errors := make(chan error, 2)
	serve := func(l net.Listener, publicOnly bool) {
		server := &http.Server{Handler: Wiki{Store: store, Token: string(token), ReaderToken: string(readerToken), Public: publicOnly, AllowedHosts: hostList, Sync: manager}, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
		go func() {
			<-ctx.Done()
			shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			server.Shutdown(shutdown)
		}()
		errors <- server.Serve(l)
	}
	log.Printf("VEDA listening on %s; public on %s; owner key saved to data directory (not printed)", *listen, *public)
	go serve(publicListener, true)
	go serve(apiListener, false)
	if err := <-errors; err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

// loadOrCreateKey never logs key material; both files are local private state.
func loadOrCreateKey(dir, name string) ([]byte, error) {
	keyPath := filepath.Join(dir, name)
	if info, e := os.Lstat(keyPath); e == nil && (!info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0) {
		return nil, ErrInvalid
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
		return nil, ErrInvalid
	}
	if _, err = hex.DecodeString(string(key)); err != nil {
		return nil, ErrInvalid
	}
	return key, nil
}
