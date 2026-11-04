package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/knowledge"
)

const jobColumns = `source_id, path, sha, fingerprint, state, attempts, next_attempt, last_error, updated_at`

func scanJob(rows interface{ Scan(...any) error }) (knowledge.Job, error) {
	var j knowledge.Job
	var state string
	var next, updated int64
	err := rows.Scan(&j.Key.SourceID, &j.Key.Path, &j.Key.SHA, &j.Fingerprint, &state, &j.Attempts, &next, &j.LastError, &updated)
	j.State, j.NextAttempt, j.Updated = knowledge.JobState(state), fromMS(next), fromMS(updated)
	return j, err
}

func (d *DB) tx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err = fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Reconcile implements knowledge.Manifest.
func (d *DB) Reconcile(ctx context.Context, fp knowledge.Fingerprint, active []knowledge.SnapshotKey, now time.Time) (knowledge.ReconcileChange, error) {
	var change knowledge.ReconcileChange
	isActive := make(map[knowledge.SnapshotKey]bool, len(active))
	for _, k := range active {
		isActive[k] = true
	}
	err := d.tx(ctx, func(tx *sql.Tx) error {
		change = knowledge.ReconcileChange{}
		rows, err := tx.QueryContext(ctx, `SELECT `+jobColumns+` FROM manifest`)
		if err != nil {
			return err
		}
		existing := map[knowledge.Snapshot]knowledge.JobState{}
		for rows.Next() {
			j, err := scanJob(rows)
			if err != nil {
				rows.Close()
				return err
			}
			existing[j.Snapshot] = j.State
		}
		if err = rows.Close(); err != nil {
			return err
		}
		for snap, state := range existing {
			if state != knowledge.JobSuperseded && (snap.Fingerprint != fp || !isActive[snap.Key]) {
				if _, err = tx.ExecContext(ctx, `UPDATE manifest SET state='superseded', updated_at=? WHERE source_id=? AND path=? AND sha=? AND fingerprint=?`,
					now.UnixMilli(), snap.Key.SourceID, snap.Key.Path, snap.Key.SHA, string(snap.Fingerprint)); err != nil {
					return err
				}
				change.Superseded = append(change.Superseded, snap)
			}
		}
		for _, k := range active {
			snap := knowledge.Snapshot{Key: k, Fingerprint: fp}
			state, ok := existing[snap]
			switch {
			case !ok:
				_, err = tx.ExecContext(ctx, `INSERT INTO manifest (source_id, path, sha, fingerprint, state, attempts, next_attempt, last_error, created_at, updated_at) VALUES (?,?,?,?,'pending',0,0,'',?,?)`,
					k.SourceID, k.Path, k.SHA, string(fp), now.UnixMilli(), now.UnixMilli())
			case state == knowledge.JobSuperseded:
				_, err = tx.ExecContext(ctx, `UPDATE manifest SET state='pending', attempts=0, next_attempt=0, last_error='', updated_at=? WHERE source_id=? AND path=? AND sha=? AND fingerprint=?`,
					now.UnixMilli(), k.SourceID, k.Path, k.SHA, string(fp))
			default:
				continue
			}
			if err != nil {
				return err
			}
			change.Pending++
		}
		return nil
	})
	return change, err
}

// Requeue implements knowledge.Manifest.
func (d *DB) Requeue(ctx context.Context, snaps []knowledge.Snapshot, now time.Time) (int, error) {
	n := 0
	err := d.tx(ctx, func(tx *sql.Tx) error {
		n = 0
		for _, s := range snaps {
			r, err := tx.ExecContext(ctx, `UPDATE manifest SET state='pending', attempts=0, next_attempt=0, last_error='', updated_at=? WHERE source_id=? AND path=? AND sha=? AND fingerprint=? AND state IN ('succeeded','failed')`,
				now.UnixMilli(), s.Key.SourceID, s.Key.Path, s.Key.SHA, string(s.Fingerprint))
			if err != nil {
				return err
			}
			c, _ := r.RowsAffected()
			n += int(c)
		}
		return nil
	})
	return n, err
}

// ResetRunning implements knowledge.Manifest.
func (d *DB) ResetRunning(ctx context.Context, now time.Time) (int, error) {
	r, err := d.db.ExecContext(ctx, `UPDATE manifest SET state='pending', next_attempt=0, updated_at=? WHERE state='running'`, now.UnixMilli())
	if err != nil {
		return 0, err
	}
	n, _ := r.RowsAffected()
	return int(n), nil
}

// Claim implements knowledge.Manifest.
func (d *DB) Claim(ctx context.Context, fp knowledge.Fingerprint, now time.Time, n int) ([]knowledge.Job, error) {
	var jobs []knowledge.Job
	err := d.tx(ctx, func(tx *sql.Tx) error {
		jobs = nil
		rows, err := tx.QueryContext(ctx, `SELECT `+jobColumns+` FROM manifest WHERE fingerprint=? AND state='pending' AND next_attempt<=? ORDER BY next_attempt, source_id, path, sha LIMIT ?`,
			string(fp), now.UnixMilli(), n)
		if err != nil {
			return err
		}
		for rows.Next() {
			j, err := scanJob(rows)
			if err != nil {
				rows.Close()
				return err
			}
			jobs = append(jobs, j)
		}
		if err = rows.Close(); err != nil {
			return err
		}
		for i := range jobs {
			jobs[i].State = knowledge.JobRunning
			jobs[i].Attempts++
			jobs[i].Updated = now
			if _, err = tx.ExecContext(ctx, `UPDATE manifest SET state='running', attempts=attempts+1, updated_at=? WHERE source_id=? AND path=? AND sha=? AND fingerprint=?`,
				now.UnixMilli(), jobs[i].Key.SourceID, jobs[i].Key.Path, jobs[i].Key.SHA, string(fp)); err != nil {
				return err
			}
		}
		return nil
	})
	return jobs, err
}

// Finish implements knowledge.Manifest.
func (d *DB) Finish(ctx context.Context, snap knowledge.Snapshot, f knowledge.Finish, now time.Time) (bool, error) {
	refund := 0
	if f.Refund {
		refund = 1
	}
	r, err := d.db.ExecContext(ctx, `UPDATE manifest SET state=?, last_error=?, next_attempt=?, attempts=MAX(attempts-?, 0), updated_at=? WHERE source_id=? AND path=? AND sha=? AND fingerprint=? AND state='running'`,
		string(f.State), f.LastError, ms(f.NextAttempt), refund, now.UnixMilli(), snap.Key.SourceID, snap.Key.Path, snap.Key.SHA, string(snap.Fingerprint))
	if err != nil {
		return false, err
	}
	n, _ := r.RowsAffected()
	return n == 1, nil
}

// NextDue implements knowledge.Manifest.
func (d *DB) NextDue(ctx context.Context, fp knowledge.Fingerprint) (time.Time, bool, error) {
	var next sql.NullInt64
	if err := d.db.QueryRowContext(ctx, `SELECT MIN(next_attempt) FROM manifest WHERE fingerprint=? AND state='pending'`, string(fp)).Scan(&next); err != nil {
		return time.Time{}, false, err
	}
	if !next.Valid {
		return time.Time{}, false, nil
	}
	return time.UnixMilli(next.Int64).UTC(), true, nil
}

// Jobs implements knowledge.Manifest.
func (d *DB) Jobs(ctx context.Context, keys []knowledge.SnapshotKey) ([]knowledge.Job, error) {
	var out []knowledge.Job
	collect := func(rows *sql.Rows, err error) error {
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			j, err := scanJob(rows)
			if err != nil {
				return err
			}
			out = append(out, j)
		}
		return rows.Err()
	}
	if len(keys) == 0 {
		err := collect(d.db.QueryContext(ctx, `SELECT `+jobColumns+` FROM manifest ORDER BY source_id, path, sha, fingerprint`))
		return out, err
	}
	for _, k := range keys {
		if err := collect(d.db.QueryContext(ctx, `SELECT `+jobColumns+` FROM manifest WHERE source_id=? AND path=? AND sha=? ORDER BY fingerprint`, k.SourceID, k.Path, k.SHA)); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Counts implements knowledge.Manifest.
func (d *DB) Counts(ctx context.Context, fp knowledge.Fingerprint) (map[knowledge.JobState]int, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT state, COUNT(*) FROM manifest WHERE fingerprint=? GROUP BY state`, string(fp))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[knowledge.JobState]int{}
	for rows.Next() {
		var state string
		var n int
		if err = rows.Scan(&state, &n); err != nil {
			return nil, err
		}
		out[knowledge.JobState(state)] = n
	}
	return out, rows.Err()
}

// SaveResult implements knowledge.Manifest. An existing result is kept.
func (d *DB) SaveResult(ctx context.Context, snap knowledge.Snapshot, extraction string, res knowledge.ExtractResult, now time.Time) error {
	data, err := json.Marshal(res)
	if err != nil {
		return err
	}
	_, err = d.db.ExecContext(ctx, `INSERT OR IGNORE INTO results (source_id, path, sha, fingerprint, extraction, data, created_at) VALUES (?,?,?,?,?,?,?)`,
		snap.Key.SourceID, snap.Key.Path, snap.Key.SHA, string(snap.Fingerprint), extraction, data, now.UnixMilli())
	return err
}

// Result implements knowledge.Manifest: the result of snap itself first,
// else the oldest result of the same key with the same extraction identity.
func (d *DB) Result(ctx context.Context, snap knowledge.Snapshot, extraction string) (knowledge.ExtractResult, bool, error) {
	var data []byte
	err := d.db.QueryRowContext(ctx, `SELECT data FROM results WHERE source_id=? AND path=? AND sha=? AND (fingerprint=? OR extraction=?) ORDER BY fingerprint<>?, created_at LIMIT 1`,
		snap.Key.SourceID, snap.Key.Path, snap.Key.SHA, string(snap.Fingerprint), extraction, string(snap.Fingerprint)).Scan(&data)
	if err == sql.ErrNoRows {
		return knowledge.ExtractResult{}, false, nil
	}
	if err != nil {
		return knowledge.ExtractResult{}, false, err
	}
	var res knowledge.ExtractResult
	if err = json.Unmarshal(data, &res); err != nil {
		return knowledge.ExtractResult{}, false, err
	}
	return res, true, nil
}

// Prune implements knowledge.Manifest.
func (d *DB) Prune(ctx context.Context, before time.Time) (int, error) {
	n := 0
	err := d.tx(ctx, func(tx *sql.Tx) error {
		r, err := tx.ExecContext(ctx, `DELETE FROM manifest WHERE state='superseded' AND updated_at<?`, before.UnixMilli())
		if err != nil {
			return err
		}
		c, _ := r.RowsAffected()
		n = int(c)
		// A result stays while any row of its snapshot key remains: another
		// fingerprint with the same extraction identity may reuse it.
		_, err = tx.ExecContext(ctx, `DELETE FROM results WHERE NOT EXISTS (SELECT 1 FROM manifest m WHERE m.source_id=results.source_id AND m.path=results.path AND m.sha=results.sha)`)
		return err
	})
	return n, err
}

const metaExtractorPipeline = "extractor_pipeline"

// ExtractorPipeline implements knowledge.Manifest.
func (d *DB) ExtractorPipeline(ctx context.Context) (json.RawMessage, bool, error) {
	var data []byte
	err := d.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE name=?`, metaExtractorPipeline).Scan(&data)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return json.RawMessage(data), true, nil
}

// SetExtractorPipeline implements knowledge.Manifest.
func (d *DB) SetExtractorPipeline(ctx context.Context, pipeline json.RawMessage, now time.Time) error {
	_, err := d.db.ExecContext(ctx, `INSERT INTO meta (name, value, updated_at) VALUES (?,?,?) ON CONFLICT(name) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		metaExtractorPipeline, []byte(pipeline), now.UnixMilli())
	return err
}

// Reindex implements knowledge.Manifest.
func (d *DB) Reindex(ctx context.Context, fp knowledge.Fingerprint, sourceID, path string, force bool, now time.Time) (int, error) {
	const scope = `m.fingerprint=? AND m.state IN ('pending','succeeded','failed') AND (?='' OR m.source_id=?) AND (?='' OR m.path=?)`
	args := []any{string(fp), sourceID, sourceID, path, path}
	n := 0
	err := d.tx(ctx, func(tx *sql.Tx) error {
		if force {
			if _, err := tx.ExecContext(ctx, `DELETE FROM results WHERE EXISTS (SELECT 1 FROM manifest m WHERE m.source_id=results.source_id AND m.path=results.path AND m.sha=results.sha AND `+scope+`)`, args...); err != nil {
				return err
			}
		}
		r, err := tx.ExecContext(ctx, `UPDATE manifest AS m SET state='pending', attempts=0, next_attempt=0, last_error='', updated_at=? WHERE `+scope, append([]any{now.UnixMilli()}, args...)...)
		if err != nil {
			return err
		}
		c, _ := r.RowsAffected()
		n = int(c)
		return nil
	})
	return n, err
}
