package app

import (
	"context"
	"errors"
	"flag"
	"log"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/knowledge"
	"github.com/Mesteriis/veda-wiki/internal/knowledge/extractorclient"
	"github.com/Mesteriis/veda-wiki/internal/knowledge/jobs"
	"github.com/Mesteriis/veda-wiki/internal/knowledge/memindex"
	"github.com/Mesteriis/veda-wiki/internal/knowledge/neo4jindex"
	"github.com/Mesteriis/veda-wiki/internal/store"
)

// Knowledge index backends: Neo4j in production; memory is a
// non-persistent development mode (the index is rebuilt from stored
// extraction results on every start).
const (
	indexNeo4j  = "neo4j"
	indexMemory = "memory"
)

type knowledgeFlags struct {
	enabled           bool
	extractorURL      string
	extractorKeyFile  string
	neo4jURI          string
	neo4jUser         string
	neo4jPasswordFile string
	index             string
	workers           int
	interval          time.Duration
}

func addKnowledgeFlags(flags *flag.FlagSet) *knowledgeFlags {
	k := &knowledgeFlags{}
	flags.BoolVar(&k.enabled, "knowledge", false, "Enable background knowledge graph indexing")
	flags.StringVar(&k.extractorURL, "knowledge-extractor-url", "", "Knowledge extractor URL: http://127.0.0.1:PORT, http://[::1]:PORT, http://localhost:PORT or https://HOST")
	flags.StringVar(&k.extractorKeyFile, "knowledge-extractor-key-file", "", "Absolute path of the private (0600) extractor service key file")
	flags.StringVar(&k.neo4jURI, "knowledge-neo4j-uri", "", "Neo4j URI: bolt:// or neo4j:// on a numeric loopback address, or bolt+s:// / neo4j+s://")
	flags.StringVar(&k.neo4jUser, "knowledge-neo4j-user", "", "Neo4j user")
	flags.StringVar(&k.neo4jPasswordFile, "knowledge-neo4j-password-file", "", "Absolute path of the private (0600) Neo4j password file")
	flags.StringVar(&k.index, "knowledge-index", indexNeo4j, "Knowledge index backend: neo4j, or memory (development only, not persistent)")
	flags.IntVar(&k.workers, "knowledge-workers", 1, "Concurrent knowledge jobs (1 to 4)")
	flags.DurationVar(&k.interval, "knowledge-reconcile-interval", knowledge.DefaultReconcileInterval, "Full knowledge reconciliation interval (30s to 24h)")
	return k
}

// set reports whether any knowledge flag other than --knowledge was given.
func knowledgeFlagsSet(flags *flag.FlagSet) bool {
	set := false
	flags.Visit(func(f *flag.Flag) {
		if strings.HasPrefix(f.Name, "knowledge-") {
			set = true
		}
	})
	return set
}

// validate checks the flags of an enabled module without reading secrets.
func (k *knowledgeFlags) validate() error {
	if _, err := extractorclient.ValidateURL(k.extractorURL); err != nil {
		return err
	}
	if k.extractorKeyFile == "" {
		return errors.New("knowledge needs --knowledge-extractor-key-file")
	}
	if k.workers < 1 || k.workers > knowledge.MaxWorkers {
		return errors.New("invalid knowledge workers (1 to 4)")
	}
	if k.interval < knowledge.MinReconcileInterval || k.interval > 24*time.Hour {
		return errors.New("invalid knowledge reconcile interval (30s to 24h)")
	}
	switch k.index {
	case indexMemory:
		if k.neo4jURI != "" || k.neo4jUser != "" || k.neo4jPasswordFile != "" {
			return errors.New("neo4j flags conflict with --knowledge-index=memory")
		}
		return nil
	case indexNeo4j:
		if err := validateNeo4jURI(k.neo4jURI); err != nil {
			return err
		}
		if k.neo4jUser == "" || k.neo4jPasswordFile == "" {
			return errors.New("knowledge needs --knowledge-neo4j-user and --knowledge-neo4j-password-file")
		}
		return nil
	}
	return errors.New("invalid knowledge index (neo4j or memory)")
}

func validateNeo4jURI(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("knowledge needs a valid --knowledge-neo4j-uri")
	}
	switch u.Scheme {
	case "bolt+s", "neo4j+s":
		return nil
	case "bolt", "neo4j":
		if ip := net.ParseIP(u.Hostname()); ip == nil || !ip.IsLoopback() || u.Port() == "" {
			return errors.New("unencrypted Neo4j URI must be a numeric loopback address with a port")
		}
		return nil
	}
	return errors.New("Neo4j URI must use bolt, neo4j, bolt+s or neo4j+s")
}

// readSecretFile reads a private secret: an absolute path to a regular
// file (not a symlink) with no group or other permissions, at most 4 KiB.
// Errors never include the path's content.
func readSecretFile(p, what string) (string, error) {
	invalid := errors.New(what + " must be an absolute path to a private (0600) regular file")
	if !filepath.IsAbs(p) {
		return "", invalid
	}
	info, err := os.Lstat(p)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 4096 {
		return "", invalid
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return "", invalid
	}
	secret := strings.TrimSpace(string(data))
	if secret == "" || strings.ContainsAny(secret, "\r\n") {
		return "", errors.New(what + " is empty or has several lines")
	}
	return secret, nil
}

// neo4jConnectTimeout bounds connecting to Neo4j. The driver connects
// lazily: an unreachable Neo4j makes the knowledge status degraded, and the
// service retries with backoff; VEDA itself is unaffected.
const neo4jConnectTimeout = 5 * time.Second

type knowledgeModule struct {
	service    *knowledge.Service
	db         *jobs.DB
	closeIndex func()
	// cancel and stopped are set by start; close cancels Run and waits for
	// it to return before closing what Run uses.
	cancel  context.CancelFunc
	stopped chan struct{}
}

// start runs the service until ctx ends or close is called; workers tracks
// it for the graceful drain.
func (m *knowledgeModule) start(ctx context.Context, workers *sync.WaitGroup) {
	ctx, m.cancel = context.WithCancel(ctx)
	m.stopped = make(chan struct{})
	workers.Add(1)
	go func() {
		defer workers.Done()
		defer close(m.stopped)
		_ = m.service.Run(ctx)
	}()
}

// close stops the service and then closes the manifest and the index. It
// waits for Run even when the drain timed out, so nothing uses a closed
// database; Run returns once its context ends (job writes after
// cancellation are bounded by knowledge.CleanupTimeout).
func (m *knowledgeModule) close() {
	if m != nil {
		if m.cancel != nil {
			m.cancel()
			<-m.stopped
		}
		_ = m.db.Close()
		if m.closeIndex != nil {
			m.closeIndex()
		}
	}
}

// openKnowledge builds the module of an enabled --knowledge: validated
// flags, the extractor key, the alias rules, the index, the extractor client
// and <data>/knowledge.sqlite. It starts nothing. Tests replace it to prove
// that a disabled module is never opened.
var openKnowledge = func(dataDir string, st *store.Store, k *knowledgeFlags) (*knowledgeModule, error) {
	if err := k.validate(); err != nil {
		return nil, err
	}
	key, err := readSecretFile(k.extractorKeyFile, "knowledge extractor key file")
	if err != nil {
		return nil, err
	}
	var index knowledge.Index
	closeIndex := func() {}
	switch k.index {
	case indexMemory:
		index = memindex.New()
	default:
		password, err := readSecretFile(k.neo4jPasswordFile, "knowledge Neo4j password file")
		if err != nil {
			return nil, err
		}
		neo, err := neo4jindex.New(neo4jindex.Config{URI: k.neo4jURI, User: k.neo4jUser, Password: password, ConnectTimeout: neo4jConnectTimeout})
		if err != nil {
			return nil, err
		}
		index = neo
		closeIndex = func() {
			ctx, cancel := context.WithTimeout(context.Background(), knowledge.CleanupTimeout)
			defer cancel()
			_ = neo.Close(ctx)
		}
	}
	schema := knowledge.SchemaV1()
	aliases, aliasDigest, err := jobs.ReadAliases(dataDir, schema)
	if err != nil {
		closeIndex()
		return nil, err
	}
	client, err := extractorclient.New(k.extractorURL, key)
	if err != nil {
		closeIndex()
		return nil, err
	}
	db, err := jobs.Open(dataDir)
	if err != nil {
		closeIndex()
		return nil, err
	}
	svc, err := knowledge.NewService(knowledge.Config{Store: st, Manifest: db, Index: index, Extractor: client,
		Schema: schema, Aliases: aliases, AliasDigest: aliasDigest, Workers: k.workers, ReconcileInterval: k.interval, Logf: log.Printf})
	if err != nil {
		db.Close()
		closeIndex()
		return nil, err
	}
	log.Printf("knowledge indexing enabled (%s index, %d workers)", k.index, k.workers)
	return &knowledgeModule{service: svc, db: db, closeIndex: closeIndex}, nil
}
