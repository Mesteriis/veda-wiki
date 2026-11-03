package app

import (
	"context"
	"errors"
	"flag"
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
	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/github"
	"github.com/Mesteriis/veda-wiki/internal/httpapi"
	"github.com/Mesteriis/veda-wiki/internal/importer"
	"github.com/Mesteriis/veda-wiki/internal/store"
	"github.com/Mesteriis/veda-wiki/internal/syncer"
	"github.com/Mesteriis/veda-wiki/internal/translation"
)

// Run starts the service with the server flags in args (the command line
// without the program name) and returns when it stops. A SIGINT or SIGTERM
// starts the bounded shutdown. If the drain times out, the writer lock stays
// held until the process exits.
func Run(ctx context.Context, args []string) (err error) {
	flags := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	data := flags.String("data", ".veda", "Private local data directory (single writer)")
	listen := flags.String("listen", "127.0.0.1:8080", "API and viewer address")
	public := flags.String("public-listen", "127.0.0.1:8081", "Public-only address")
	httpsListen := flags.String("https-listen", "", "Optional additional private HTTPS address")
	hosts := flags.String("allowed-hosts", "localhost,127.0.0.1,::1", "Allowed HTTP Host names")
	localImportSource := flags.String("local-import-source", "", "Allow Linux root on the reader socket to import this existing private local source")
	readerSocket := flags.String("reader-socket", "", "Optional private Unix socket for read-only local clients")
	readerKeyRequired := flags.Bool("reader-socket-require-key", false, "Require a scoped key on the private reader socket")
	tlsCert := flags.String("tls-cert", "", "TLS certificate for the private listener")
	tlsKey := flags.String("tls-key", "", "TLS private key for the private listener")
	interval := flags.Duration("sync-interval", 5*time.Minute, "Background repository check interval (5s to 1h)")
	translationURL := flags.String("translation-local-url", "", "Optional numeric loopback translation service URL")
	translationModel := flags.String("translation-model", "", "Local model for Russian documentation drafts")
	translationTokenFile := flags.String("translation-token-file", "", "Optional private file containing the local translation service bearer")
	translationProtocol := flags.String("translation-protocol", "ollama", "Local translation protocol: ollama, mlx or openai")
	translationInterval := flags.Duration("translation-interval", time.Minute, "Local translation check interval (5s to 1h)")
	_ = flags.Parse(args)
	if *readerKeyRequired && *readerSocket == "" {
		return errors.New("reader socket key requirement needs a reader socket")
	}
	if *translationTokenFile != "" && (*translationURL == "" || !filepath.IsAbs(*translationTokenFile) || filepath.Clean(*translationTokenFile) != *translationTokenFile) {
		return errors.New("invalid local translation credential configuration")
	}
	if (*translationURL == "") != (*translationModel == "") || (*translationProtocol != "ollama" && *translationProtocol != "mlx" && *translationProtocol != "openai") || *translationInterval < 5*time.Second || *translationInterval > time.Hour {
		return errors.New("invalid local translation configuration")
	}
	if *localImportSource != "" && (*readerSocket == "" || !domain.ValidID(*localImportSource)) {
		return errors.New("invalid local import source")
	}
	if (*tlsCert == "") != (*tlsKey == "") {
		return errors.New("both tls-cert and tls-key are required")
	}
	if *httpsListen != "" && *tlsCert == "" {
		return errors.New("additional HTTPS listener requires a certificate and key")
	}
	if *httpsListen != "" {
		host, _, err := net.SplitHostPort(*listen)
		ip := net.ParseIP(host)
		if err != nil || !(strings.EqualFold(host, "localhost") || ip != nil && ip.IsLoopback()) {
			return errors.New("HTTP listener must be loopback when additional HTTPS is enabled")
		}
	}
	if *interval < 5*time.Second || *interval > time.Hour {
		return errors.New("invalid sync interval")
	}
	if err := store.PrivateDataDir(*data); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(*data, "writer.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer func() { releaseWriterLock(lock, err) }()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("data directory already in use")
	}
	// Open only after taking the process lock, including SQLite recovery.
	st, err := store.Open(*data)
	if err != nil {
		return err
	}
	// One set of import observations serves every listener and the syncer.
	observations := &importer.Observations{}
	token, err := loadOrCreateKey(*data, "owner.key")
	if err != nil {
		return errors.New("owner key unavailable or invalid")
	}
	readerToken, err := loadOrCreateKey(*data, "reader.key")
	if err != nil {
		return errors.New("reader key unavailable or invalid")
	}
	hostList := strings.Split(*hosts, ",")
	for i := range hostList {
		hostList[i] = strings.TrimSpace(hostList[i])
		if hostList[i] == "" {
			return errors.New("empty allowed host")
		}
	}
	apiListener, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	publicListener, err := net.Listen("tcp", *public)
	if err != nil {
		apiListener.Close()
		return err
	}
	var httpsListener net.Listener
	if *httpsListen != "" {
		httpsListener, err = net.Listen("tcp", *httpsListen)
		if err != nil {
			apiListener.Close()
			publicListener.Close()
			return err
		}
	}
	var socketListener net.Listener
	if *readerSocket != "" {
		socketListener, err = listenReaderSocket(*readerSocket)
		if err != nil {
			apiListener.Close()
			publicListener.Close()
			return errors.New("reader socket unavailable")
		}
		defer socketListener.Close()
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	var workers sync.WaitGroup
	imp := importer.Service{Store: st, Observations: observations}
	manager := syncer.New(st, github.New(), &imp)
	manager.LocalImportSource = *localImportSource
	if *translationURL != "" {
		worker, err := translation.NewWorker(st, *translationURL, *translationModel, *translationProtocol)
		if err != nil {
			return err
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
		cfg := httpapi.Config{OwnerToken: string(token), ReaderToken: string(readerToken), Public: publicOnly, AllowedHosts: hostList}
		deps := httpapi.Deps{Store: st, Observations: observations, Sessions: sessions, AdminSessions: adminSessions, AccessSessions: accessSessions, Sync: manager}
		if readerOnly {
			cfg.OwnerToken = ""
			cfg.ReaderToken = ""
			deps.Sessions = nil
			deps.AdminSessions = nil
			deps.AccessSessions = nil
			cfg.ReaderLocal = true
			cfg.ReaderKeyRequired = *readerKeyRequired
			cfg.LocalImportSource = *localImportSource
		}
		handler := httpapi.New(cfg, deps)
		server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
		if readerOnly && *localImportSource != "" {
			server.ConnContext = func(ctx context.Context, conn net.Conn) context.Context {
				return context.WithValue(ctx, httpapi.LocalRootKey{}, peerIsRoot(conn))
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
	return serveUntilStopped(ctx, stop, endpoints, &workers)
}
