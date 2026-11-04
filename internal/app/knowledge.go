package app

import (
	"errors"
	"flag"
	"log"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/knowledge"
	"github.com/Mesteriis/veda-wiki/internal/knowledge/extractorclient"
	"github.com/Mesteriis/veda-wiki/internal/knowledge/jobs"
	"github.com/Mesteriis/veda-wiki/internal/knowledge/memindex"
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
	flags.StringVar(&k.extractorURL, "knowledge-extractor-url", "", "Knowledge extractor URL: http://127.0.0.1:PORT or https://HOST")
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

type knowledgeModule struct {
	service *knowledge.Service
	db      *jobs.DB
}

func (m *knowledgeModule) close() {
	if m != nil {
		_ = m.db.Close()
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
	switch k.index {
	case indexMemory:
		index = memindex.New()
	default:
		if _, err = readSecretFile(k.neo4jPasswordFile, "knowledge Neo4j password file"); err != nil {
			return nil, err
		}
		return nil, errors.New("the Neo4j knowledge index is not available in this build; use --knowledge-index=memory for development")
	}
	schema := knowledge.SchemaV1()
	aliases, aliasDigest, err := jobs.ReadAliases(dataDir, schema)
	if err != nil {
		return nil, err
	}
	client, err := extractorclient.New(k.extractorURL, key)
	if err != nil {
		return nil, err
	}
	db, err := jobs.Open(dataDir)
	if err != nil {
		return nil, err
	}
	svc, err := knowledge.NewService(knowledge.Config{Store: st, Manifest: db, Index: index, Extractor: client,
		Schema: schema, Aliases: aliases, AliasDigest: aliasDigest, Workers: k.workers, ReconcileInterval: k.interval, Logf: log.Printf})
	if err != nil {
		db.Close()
		return nil, err
	}
	log.Printf("knowledge indexing enabled (%s index, %d workers)", k.index, k.workers)
	return &knowledgeModule{service: svc, db: db}, nil
}
