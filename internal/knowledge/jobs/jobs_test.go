package jobs_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/knowledge"
	"github.com/Mesteriis/veda-wiki/internal/knowledge/jobs"
	_ "modernc.org/sqlite"
)

var (
	ctx = context.Background()
	t0  = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	fp1 = knowledge.Fingerprint("kg1-one")
	fp2 = knowledge.Fingerprint("kg1-two")
	ka  = knowledge.SnapshotKey{SourceID: "s", Path: "a.md", SHA: "a1"}
	kb  = knowledge.SnapshotKey{SourceID: "s", Path: "b.md", SHA: "b1"}
)

func open(t *testing.T) (*jobs.DB, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := jobs.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, dir
}

func TestOpenCreatesPrivateMigratedFile(t *testing.T) {
	db, dir := open(t)
	info, err := os.Lstat(filepath.Join(dir, jobs.File))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("database is not 0600", err)
	}
	if v, err := db.Version(ctx); err != nil || v != 2 {
		t.Fatal(v, err)
	}
	db.Close()
	again, err := jobs.Open(dir) // idempotent migrations
	if err != nil {
		t.Fatal(err)
	}
	again.Close()
	raw, _ := sql.Open("sqlite", filepath.Join(dir, jobs.File))
	if _, err = raw.Exec(`INSERT INTO migrations (version, applied_at) VALUES (99, 0)`); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	if _, err = jobs.Open(dir); err == nil {
		t.Fatal("database of a newer VEDA opened")
	}
	if err = os.Chmod(filepath.Join(dir, jobs.File), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = jobs.Open(dir); err == nil {
		t.Fatal("world-readable database opened")
	}
}

func state(t *testing.T, db *jobs.DB, s knowledge.Snapshot) knowledge.Job {
	t.Helper()
	all, err := db.Jobs(ctx, []knowledge.SnapshotKey{s.Key})
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range all {
		if j.Snapshot == s {
			return j
		}
	}
	t.Fatalf("no row %+v", s)
	return knowledge.Job{}
}

func TestStateMachine(t *testing.T) {
	db, _ := open(t)
	a1, b1 := knowledge.Snapshot{Key: ka, Fingerprint: fp1}, knowledge.Snapshot{Key: kb, Fingerprint: fp1}
	change, err := db.Reconcile(ctx, fp1, []knowledge.SnapshotKey{ka, kb}, t0)
	if err != nil || change.Pending != 2 || len(change.Superseded) != 0 {
		t.Fatal(change, err)
	}
	if change, _ = db.Reconcile(ctx, fp1, []knowledge.SnapshotKey{ka, kb}, t0); change.Pending != 0 {
		t.Fatal("reconcile is not idempotent", change)
	}
	claimed, err := db.Claim(ctx, fp1, t0, 1)
	if err != nil || len(claimed) != 1 || claimed[0].Snapshot != a1 || claimed[0].Attempts != 1 || state(t, db, a1).State != knowledge.JobRunning {
		t.Fatal(claimed, err)
	}
	// Retry with backoff; not due before NextAttempt.
	if ok, _ := db.Finish(ctx, a1, knowledge.Finish{State: knowledge.JobPending, LastError: "invalid_result", NextAttempt: t0.Add(time.Minute)}, t0); !ok {
		t.Fatal("finish refused")
	}
	if due, ok, _ := db.NextDue(ctx, fp1); !ok || due.After(t0) {
		t.Fatal("next due", due, ok)
	}
	if c, _ := db.Claim(ctx, fp1, t0, 5); len(c) != 1 || c[0].Snapshot != b1 {
		t.Fatal("claimed a job that is not due", c)
	}
	if c, _ := db.Claim(ctx, fp1, t0.Add(time.Minute), 5); len(c) != 1 || c[0].Attempts != 2 {
		t.Fatal("due retry not claimed", c)
	}
	// An outage refunds the attempt.
	if ok, _ := db.Finish(ctx, a1, knowledge.Finish{State: knowledge.JobPending, Refund: true}, t0); !ok || state(t, db, a1).Attempts != 1 {
		t.Fatal("refund", state(t, db, a1))
	}
	// Crash: running rows go back to pending.
	if n, _ := db.ResetRunning(ctx, t0); n != 1 || state(t, db, b1).State != knowledge.JobPending {
		t.Fatal("reset running", n)
	}
	// b disappears: superseded; a slow worker cannot finish it afterwards.
	claimed, _ = db.Claim(ctx, fp1, t0, 5)
	if len(claimed) != 2 {
		t.Fatal(claimed)
	}
	change, _ = db.Reconcile(ctx, fp1, []knowledge.SnapshotKey{ka}, t0)
	if len(change.Superseded) != 1 || change.Superseded[0] != b1 {
		t.Fatal(change)
	}
	if ok, _ := db.Finish(ctx, b1, knowledge.Finish{State: knowledge.JobSucceeded}, t0); ok || state(t, db, b1).State != knowledge.JobSuperseded {
		t.Fatal("finish overwrote a superseded row")
	}
	if ok, _ := db.Finish(ctx, a1, knowledge.Finish{State: knowledge.JobSucceeded}, t0); !ok {
		t.Fatal("finish a1")
	}
	// b comes back (a revert): pending again with no attempts.
	if change, _ = db.Reconcile(ctx, fp1, []knowledge.SnapshotKey{ka, kb}, t0); change.Pending != 1 || state(t, db, b1).Attempts != 0 {
		t.Fatal("revert", change, state(t, db, b1))
	}
	// Fingerprint change: everything old superseded, new rows pending.
	change, _ = db.Reconcile(ctx, fp2, []knowledge.SnapshotKey{ka, kb}, t0)
	if len(change.Superseded) != 2 || change.Pending != 2 {
		t.Fatal("fingerprint change", change)
	}
	counts, _ := db.Counts(ctx, fp2)
	if counts[knowledge.JobPending] != 2 {
		t.Fatal(counts)
	}
	if n, _ := db.Requeue(ctx, []knowledge.Snapshot{a1}, t0); n != 0 {
		t.Fatal("requeued a superseded row")
	}
}

func TestResultsAreImmutableAndReusedByExtractionIdentity(t *testing.T) {
	db, _ := open(t)
	a1, a2 := knowledge.Snapshot{Key: ka, Fingerprint: fp1}, knowledge.Snapshot{Key: ka, Fingerprint: fp2}
	first := knowledge.ExtractResult{SchemaVersion: 1, Model: knowledge.ModelInfo{ID: "m", Revision: "r1"}}
	second := knowledge.ExtractResult{SchemaVersion: 1, Model: knowledge.ModelInfo{ID: "m", Revision: "r2"}}
	if err := db.SaveResult(ctx, a1, "x1", first, t0); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveResult(ctx, a1, "x1", second, t0); err != nil {
		t.Fatal(err)
	}
	if got, ok, _ := db.Result(ctx, a1, "x1"); !ok || got.Model.Revision != "r1" {
		t.Fatal("result was replaced", got)
	}
	if got, ok, _ := db.Result(ctx, a2, "x1"); !ok || got.Model.Revision != "r1" {
		t.Fatal("same extraction identity not reused", got)
	}
	if _, ok, _ := db.Result(ctx, a2, "x2"); ok {
		t.Fatal("another extraction identity reused")
	}
	// Prune drops old superseded rows and results no row refers to.
	if _, err := db.Reconcile(ctx, fp1, []knowledge.SnapshotKey{ka}, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Reconcile(ctx, fp1, nil, t0); err != nil {
		t.Fatal(err)
	}
	if n, _ := db.Prune(ctx, t0.Add(time.Second)); n != 1 {
		t.Fatal("prune", n)
	}
	if _, ok, _ := db.Result(ctx, a1, "x1"); ok {
		t.Fatal("orphan result kept")
	}
}

func TestReadAliases(t *testing.T) {
	dir := t.TempDir()
	if a, digest, err := jobs.ReadAliases(dir, knowledge.SchemaV1()); err != nil || len(a) != 0 || digest != "" {
		t.Fatal("missing file", err)
	}
	p := filepath.Join(dir, knowledge.AliasesFile)
	if err := os.WriteFile(p, []byte(`{"version":1,"aliases":{"service":{"PG":"postgres"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	a, digest, err := jobs.ReadAliases(dir, knowledge.SchemaV1())
	if err != nil || a["service"]["pg"] != "postgres" || digest == "" {
		t.Fatal(a, err)
	}
	if err = os.Chmod(p, 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err = jobs.ReadAliases(dir, knowledge.SchemaV1()); err == nil {
		t.Fatal("world-readable aliases accepted")
	}
}

func TestReindexScopesAndForce(t *testing.T) {
	db, _ := open(t)
	kc := knowledge.SnapshotKey{SourceID: "other", Path: "a.md", SHA: "c1"}
	if _, err := db.Reconcile(ctx, fp1, []knowledge.SnapshotKey{ka, kb, kc}, t0); err != nil {
		t.Fatal(err)
	}
	claimed, _ := db.Claim(ctx, fp1, t0, 3)
	for _, j := range claimed {
		if j.Key != kb { // b stays running
			_, _ = db.Finish(ctx, j.Snapshot, knowledge.Finish{State: knowledge.JobSucceeded}, t0)
		}
		_ = db.SaveResult(ctx, j.Snapshot, "x", knowledge.ExtractResult{SchemaVersion: 1}, t0)
	}
	if n, err := db.Reindex(ctx, fp1, "s", "a.md", false, t0); err != nil || n != 1 {
		t.Fatal("article scope", n, err)
	}
	if _, ok, _ := db.Result(ctx, knowledge.Snapshot{Key: ka, Fingerprint: fp1}, "x"); !ok {
		t.Fatal("non-forced reindex dropped a result")
	}
	if n, _ := db.Reindex(ctx, fp1, "s", "", true, t0); n != 1 { // a only: b is running
		t.Fatal("source scope", n)
	}
	if _, ok, _ := db.Result(ctx, knowledge.Snapshot{Key: ka, Fingerprint: fp1}, "x"); ok {
		t.Fatal("forced reindex kept the result")
	}
	if _, ok, _ := db.Result(ctx, knowledge.Snapshot{Key: kc, Fingerprint: fp1}, "x"); !ok {
		t.Fatal("forced reindex of one source dropped another source's result")
	}
	if n, _ := db.Reindex(ctx, fp1, "", "", false, t0); n != 2 {
		t.Fatal("all scope", n)
	}
	if n, _ := db.Reindex(ctx, fp2, "", "", true, t0); n != 0 {
		t.Fatal("other fingerprint touched", n)
	}
}
