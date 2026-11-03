package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"github.com/Mesteriis/veda-wiki/internal/github"
	"github.com/Mesteriis/veda-wiki/internal/translation"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/auth"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "createsuperuser" {
		if err := createSuperuserCommand(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "migrate-sqlite" {
		if err := sqliteMigrateCommand(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "github" {
		if e := githubCommand(os.Args[2:]); e != nil {
			log.Fatal(e)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "access-key" {
		if e := accessKeyCommand(os.Args[2:], os.Stdout); e != nil {
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
	httpsListen := flag.String("https-listen", "", "Optional additional private HTTPS address")
	hosts := flag.String("allowed-hosts", "localhost,127.0.0.1,::1", "Allowed HTTP Host names")
	localImportSource := flag.String("local-import-source", "", "Allow Linux root on the reader socket to import this existing private local source")
	readerSocket := flag.String("reader-socket", "", "Optional private Unix socket for read-only local clients")
	readerKeyRequired := flag.Bool("reader-socket-require-key", false, "Require a scoped key on the private reader socket")
	tlsCert := flag.String("tls-cert", "", "TLS certificate for the private listener")
	tlsKey := flag.String("tls-key", "", "TLS private key for the private listener")
	interval := flag.Duration("sync-interval", 5*time.Minute, "Background repository check interval (5s to 1h)")
	translationURL := flag.String("translation-local-url", "", "Optional numeric loopback translation service URL")
	translationModel := flag.String("translation-model", "", "Local model for Russian documentation drafts")
	translationTokenFile := flag.String("translation-token-file", "", "Optional private file containing the local translation service bearer")
	translationProtocol := flag.String("translation-protocol", "ollama", "Local translation protocol: ollama, mlx or openai")
	translationInterval := flag.Duration("translation-interval", time.Minute, "Local translation check interval (5s to 1h)")
	flag.Parse()
	if *readerKeyRequired && *readerSocket == "" {
		log.Fatal("reader socket key requirement needs a reader socket")
	}
	if *translationTokenFile != "" && (*translationURL == "" || !filepath.IsAbs(*translationTokenFile) || filepath.Clean(*translationTokenFile) != *translationTokenFile) {
		log.Fatal("invalid local translation credential configuration")
	}
	if (*translationURL == "") != (*translationModel == "") || (*translationProtocol != "ollama" && *translationProtocol != "mlx" && *translationProtocol != "openai") || *translationInterval < 5*time.Second || *translationInterval > time.Hour {
		log.Fatal("invalid local translation configuration")
	}
	if *localImportSource != "" && (*readerSocket == "" || !identifier.MatchString(*localImportSource)) {
		log.Fatal("invalid local import source")
	}
	if (*tlsCert == "") != (*tlsKey == "") {
		log.Fatal("both tls-cert and tls-key are required")
	}
	if *httpsListen != "" && *tlsCert == "" {
		log.Fatal("additional HTTPS listener requires a certificate and key")
	}
	if *httpsListen != "" {
		host, _, err := net.SplitHostPort(*listen)
		ip := net.ParseIP(host)
		if err != nil || !(strings.EqualFold(host, "localhost") || ip != nil && ip.IsLoopback()) {
			log.Fatal("HTTP listener must be loopback when additional HTTPS is enabled")
		}
	}
	if *interval < 5*time.Second || *interval > time.Hour {
		log.Fatal("invalid sync interval")
	}
	if err := privateDataDir(*data); err != nil {
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
	// Open only after taking the process lock, including SQLite recovery.
	store, err := openStore(*data)
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
	var httpsListener net.Listener
	if *httpsListen != "" {
		httpsListener, err = net.Listen("tcp", *httpsListen)
		if err != nil {
			apiListener.Close()
			publicListener.Close()
			log.Fatal(err)
		}
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
	var workers sync.WaitGroup
	manager := store.syncer(github.New())
	manager.LocalImportSource = *localImportSource
	if *translationURL != "" {
		worker, err := translation.NewWorker(store.Store, *translationURL, *translationModel, *translationProtocol)
		if err != nil {
			log.Fatal(err)
		}
		worker.TokenFile = *translationTokenFile
		workers.Add(1)
		go func() { defer workers.Done(); worker.Run(ctx, *translationInterval) }()
	}
	sessions := &auth.Sessions{}
	adminSessions := &auth.Sessions{}
	accessSessions := &auth.Sessions{}
	workers.Add(1)
	go func() { defer workers.Done(); manager.Run(ctx, *interval) }()
	makeServer := func(l net.Listener, publicOnly, readerOnly bool) servedListener {
		handler := Wiki{Store: store, Token: string(token), ReaderToken: string(readerToken), Sessions: sessions, AdminSessions: adminSessions, AccessSessions: accessSessions, Public: publicOnly, AllowedHosts: hostList, Sync: manager}
		if readerOnly {
			handler.Token = ""
			handler.ReaderToken = ""
			handler.Sessions = nil
			handler.AdminSessions = nil
			handler.AccessSessions = nil
			handler.ReaderLocal = true
			handler.ReaderKeyRequired = *readerKeyRequired
			handler.LocalImportSource = *localImportSource
		}
		server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
		if readerOnly && *localImportSource != "" {
			server.ConnContext = func(ctx context.Context, conn net.Conn) context.Context {
				return context.WithValue(ctx, localRootKey{}, peerIsRoot(conn))
			}
		}
		endpoint := servedListener{server: server, listener: l}
		if !publicOnly && !readerOnly && (httpsListener == nil || l == httpsListener) {
			endpoint.cert, endpoint.key = *tlsCert, *tlsKey
		}
		return endpoint
	}
	log.Printf("VEDA listening on %s; public on %s; owner key saved to data directory (not printed)", *listen, *public)
	endpoints := []servedListener{makeServer(publicListener, true, false), makeServer(apiListener, false, false)}
	if httpsListener != nil {
		endpoints = append(endpoints, makeServer(httpsListener, false, false))
	}
	if socketListener != nil {
		endpoints = append(endpoints, makeServer(socketListener, false, true))
	}
	if err := serveUntilStopped(ctx, stop, endpoints, &workers); err != nil {
		log.Fatal(err)
	}
}

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
