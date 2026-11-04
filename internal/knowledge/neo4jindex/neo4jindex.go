// Package neo4jindex stores the derived knowledge index in Neo4j (driver v6,
// Community edition). The model, per snapshot of one text under one
// pipeline fingerprint:
//
//	(:Snapshot {id, key, source_id, path, sha, fp, generation, doc_node, complete})
//	(:Snapshot)-[:HAS]->(:Evidence {id, block_id, quote, start, end, src_start, src_end, exact})
//	(:Mention {id, seq, type, text, surface, score, res_method, res_basis})-[:AT]->(:Evidence)
//	(:Mention)-[:OF]->(:Entity {id, type, key})
//	(:Claim {id, seq, claim_id, type, status, markers, score, head_score, tail_score})-[:AT]->(:Evidence)
//	(:Claim)-[:HEAD]->(:Entity), (:Claim)-[:TAIL]->(:Entity)
//
// Node IDs of snapshots, evidence, mentions and claims are derived from the
// fingerprint, the snapshot key and the position in the published graph,
// never from model-local IDs, so a republish replaces nodes instead of
// adding them. A snapshot is published in one write transaction that sets
// complete=true last, and every read starts from complete snapshots of the
// request's allowed set (UNWIND $allowed) under the active fingerprint: no
// evidence of another snapshot is matched, aggregated or traversed. Relation
// and entity types are validated against the schema before publication and
// are stored as property values; Cypher text is never built from data.
package neo4jindex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j/config"

	"github.com/Mesteriis/veda-wiki/internal/knowledge"
)

// Default timeouts. Reads are bounded by QueryTimeout; a publish of a
// large snapshot by PublishTimeout.
const (
	DefaultConnectTimeout = 5 * time.Second
	DefaultQueryTimeout   = 10 * time.Second
	DefaultPublishTimeout = 60 * time.Second
	// batchSize bounds the rows of one UNWIND in a publish.
	batchSize = 500
)

// ErrUnavailable wraps every driver or server failure, so callers can tell
// an outage from ErrNotFound. Messages never carry credentials.
var ErrUnavailable = errors.New("neo4j knowledge index unavailable")

// Config configures the index. Password is used only for authentication.
type Config struct {
	URI      string
	User     string
	Password string
	// Database is the Neo4j database; empty selects the server default.
	Database       string
	ConnectTimeout time.Duration
	QueryTimeout   time.Duration
	PublishTimeout time.Duration
	// Schema validates published graphs; zero selects knowledge.SchemaV1.
	Schema knowledge.Schema
}

// Index is a knowledge.Index over Neo4j.
type Index struct {
	driver   neo4j.Driver
	database string
	query    time.Duration
	publish  time.Duration
	schema   knowledge.Schema

	mu          sync.Mutex
	schemaReady bool
}

var _ knowledge.Index = (*Index)(nil)

// New creates the driver without connecting: the first call connects, so a
// Neo4j outage at start only makes the index unavailable until it recovers.
// The driver logs nothing (its default logger is void) and sends no
// telemetry.
func New(cfg Config) (*Index, error) {
	if cfg.URI == "" || cfg.User == "" || cfg.Password == "" {
		return nil, errors.New("neo4j index needs a URI, a user and a password")
	}
	connect := orDefault(cfg.ConnectTimeout, DefaultConnectTimeout)
	driver, err := neo4j.NewDriver(cfg.URI, neo4j.BasicAuth(cfg.User, cfg.Password, ""), func(c *config.Config) {
		c.SocketConnectTimeout = connect
		c.ConnectionAcquisitionTimeout = connect
		c.MaxTransactionRetryTime = connect
		c.MaxConnectionPoolSize = 16
		c.TelemetryDisabled = true
	})
	if err != nil {
		return nil, errors.New("neo4j index: invalid driver configuration")
	}
	schema := cfg.Schema
	if schema.Version == "" {
		schema = knowledge.SchemaV1()
	}
	return &Index{driver: driver, database: cfg.Database, schema: schema,
		query: orDefault(cfg.QueryTimeout, DefaultQueryTimeout), publish: orDefault(cfg.PublishTimeout, DefaultPublishTimeout)}, nil
}

func orDefault(d, def time.Duration) time.Duration {
	if d <= 0 {
		return def
	}
	return d
}

// Close closes the driver.
func (ix *Index) Close(ctx context.Context) error { return ix.driver.Close(ctx) }

// Status implements knowledge.Index: it creates the constraints once and
// runs a trivial query.
func (ix *Index) Status(ctx context.Context) error {
	if err := ix.ensureSchema(ctx); err != nil {
		return err
	}
	return ix.read(ctx, func(tx neo4j.ManagedTransaction) error {
		_, err := collect(ctx, tx, "RETURN 1 AS ok", nil)
		return err
	})
}

// schemaStatements are idempotent and available in Community: uniqueness
// constraints (each backed by a range index) and one range index.
var schemaStatements = []string{
	"CREATE CONSTRAINT veda_snapshot_id IF NOT EXISTS FOR (n:Snapshot) REQUIRE n.id IS UNIQUE",
	"CREATE CONSTRAINT veda_evidence_id IF NOT EXISTS FOR (n:Evidence) REQUIRE n.id IS UNIQUE",
	"CREATE CONSTRAINT veda_mention_id IF NOT EXISTS FOR (n:Mention) REQUIRE n.id IS UNIQUE",
	"CREATE CONSTRAINT veda_claim_id IF NOT EXISTS FOR (n:Claim) REQUIRE n.id IS UNIQUE",
	"CREATE CONSTRAINT veda_entity_id IF NOT EXISTS FOR (n:Entity) REQUIRE n.id IS UNIQUE",
	"CREATE INDEX veda_snapshot_complete IF NOT EXISTS FOR (n:Snapshot) ON (n.complete)",
}

func (ix *Index) ensureSchema(ctx context.Context) error {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if ix.schemaReady {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, ix.query)
	defer cancel()
	session := ix.session(ctx, neo4j.AccessModeWrite)
	defer session.Close(ctx)
	for _, stmt := range schemaStatements {
		res, err := session.Run(ctx, stmt, nil, neo4j.WithTxTimeout(ix.query))
		if err == nil {
			_, err = res.Consume(ctx)
		}
		if err != nil {
			return unavailable(err)
		}
	}
	ix.schemaReady = true
	return nil
}

func (ix *Index) session(ctx context.Context, mode neo4j.AccessMode) neo4j.Session {
	return ix.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: mode, DatabaseName: ix.database})
}

// read runs fn in one read transaction bounded by the query timeout.
func (ix *Index) read(ctx context.Context, fn func(tx neo4j.ManagedTransaction) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, ix.query)
	defer cancel()
	session := ix.session(ctx, neo4j.AccessModeRead)
	defer session.Close(ctx)
	_, err := session.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		return nil, fn(tx)
	}, neo4j.WithTxTimeout(ix.query))
	return unavailable(err)
}

// write runs fn in one write transaction bounded by timeout.
func (ix *Index) write(ctx context.Context, timeout time.Duration, fn func(tx neo4j.ManagedTransaction) error) error {
	if err := ix.ensureSchema(ctx); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	session := ix.session(ctx, neo4j.AccessModeWrite)
	defer session.Close(ctx)
	_, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		return nil, fn(tx)
	}, neo4j.WithTxTimeout(timeout))
	return unavailable(err)
}

// invalidGraph marks errors of the published data, not of Neo4j.
type invalidGraph struct{ msg string }

func (e invalidGraph) Error() string { return "neo4j index: invalid graph: " + e.msg }

// unavailable maps driver errors to ErrUnavailable; ErrNotFound, invalid
// graphs and context errors pass through.
func unavailable(err error) error {
	var invalid invalidGraph
	switch {
	case err == nil, errors.Is(err, knowledge.ErrNotFound), errors.As(err, &invalid):
		return err
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	return fmt.Errorf("%w: %s", ErrUnavailable, describe(err))
}

// describe names the failure class without echoing server text, which may
// quote parameters.
func describe(err error) string {
	var neoErr *neo4j.Neo4jError
	switch {
	case errors.As(err, &neoErr):
		return "server error " + neoErr.Code
	case neo4j.IsConnectivityError(err):
		return "connectivity error"
	}
	return "driver error"
}

func collect(ctx context.Context, tx neo4j.ManagedTransaction, cypher string, params map[string]any) ([]*neo4j.Record, error) {
	res, err := tx.Run(ctx, cypher, params)
	if err != nil {
		return nil, err
	}
	return res.Collect(ctx)
}

// SnapshotID is the node ID of a snapshot under a fingerprint.
func SnapshotID(fp knowledge.Fingerprint, k knowledge.SnapshotKey) string {
	sum := sha256.Sum256([]byte(string(fp) + "\x00" + k.SourceID + "\x00" + k.Path + "\x00" + k.SHA))
	return "snp-" + hex.EncodeToString(sum[:20])
}

// allowedIDs are the snapshot IDs of the allowed set under fp.
func allowedIDs(allowed knowledge.AllowedSet, fp knowledge.Fingerprint) []string {
	keys := allowed.Keys()
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = SnapshotID(fp, k)
	}
	return out
}
