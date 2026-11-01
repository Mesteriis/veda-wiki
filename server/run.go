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
	localImportSource := flag.String("local-import-source", "", "Allow Linux root on the reader socket to import this existing private local source")
	readerSocket := flag.String("reader-socket", "", "Optional private Unix socket for read-only local clients")
	tlsCert := flag.String("tls-cert", "", "TLS certificate for the private listener")
	tlsKey := flag.String("tls-key", "", "TLS private key for the private listener")
	interval := flag.Duration("sync-interval", 5*time.Minute, "Background repository check interval (5s to 1h)")
	translationURL := flag.String("translation-local-url", "", "Optional numeric loopback Ollama URL")
	translationModel := flag.String("translation-model", "", "Local Ollama model for Russian documentation drafts")
	translationInterval := flag.Duration("translation-interval", time.Minute, "Local translation check interval (5s to 1h)")
	flag.Parse()
	if (*translationURL == "") != (*translationModel == "") || *translationInterval < 5*time.Second || *translationInterval > time.Hour {
		log.Fatal("invalid local translation configuration")
	}
	if *localImportSource != "" && (*readerSocket == "" || !identifier.MatchString(*localImportSource)) {
		log.Fatal("invalid local import source")
	}
	if (*tlsCert == "") != (*tlsKey == "") {
		log.Fatal("both tls-cert and tls-key are required")
	}
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
	var socketListener net.Listener
	if *readerSocket != "" {
		socketListener, err = listenReaderSocket(*readerSocket)
		if err != nil {
			apiListener.Close()
			publicListener.Close()
			log.Fatal("reader socket unavailable")
		}
		defer socketListener.Close()
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	manager := newSyncManager(store, newGitHubClient())
	if *translationURL != "" {
		worker, err := newTranslationWorker(store, *translationURL, *translationModel)
		if err != nil {
			log.Fatal(err)
		}
		go worker.Run(ctx, *translationInterval)
	}
	sessions := &readerSessions{}
	go manager.Run(ctx, *interval)
	errors := make(chan error, 3)
	serve := func(l net.Listener, publicOnly, readerOnly bool) {
		handler := Wiki{Store: store, Token: string(token), ReaderToken: string(readerToken), Sessions: sessions, Public: publicOnly, AllowedHosts: hostList, Sync: manager}
		if readerOnly {
			handler.Token = ""
			handler.ReaderToken = ""
			handler.Sessions = nil
			handler.ReaderLocal = true
			handler.LocalImportSource = *localImportSource
		}
		server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
		if readerOnly && *localImportSource != "" {
			server.ConnContext = func(ctx context.Context, conn net.Conn) context.Context {
				return context.WithValue(ctx, localRootKey{}, peerIsRoot(conn))
			}
		}
		go func() {
			<-ctx.Done()
			shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			server.Shutdown(shutdown)
		}()
		if !publicOnly && !readerOnly && *tlsCert != "" {
			errors <- server.ServeTLS(l, *tlsCert, *tlsKey)
		} else {
			errors <- server.Serve(l)
		}
	}
	log.Printf("VEDA listening on %s; public on %s; owner key saved to data directory (not printed)", *listen, *public)
	go serve(publicListener, true, false)
	go serve(apiListener, false, false)
	if socketListener != nil {
		go serve(socketListener, false, true)
	}
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

// The parent directory must belong to the service user and be inaccessible to
// others. Never remove an existing path, which could belong to another process.
func listenReaderSocket(name string) (net.Listener, error) {
	if !filepath.IsAbs(name) || filepath.Clean(name) != name {
		return nil, ErrInvalid
	}
	parent, err := os.Lstat(filepath.Dir(name))
	if err != nil || !parent.IsDir() || parent.Mode().Perm()&0077 != 0 {
		return nil, ErrInvalid
	}
	stat, ok := parent.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Getuid() {
		return nil, ErrInvalid
	}
	if _, err = os.Lstat(name); err == nil || !os.IsNotExist(err) {
		return nil, ErrInvalid
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

// localRootKey is set only from kernel credentials on the Unix listener.
type localRootKey struct{}
