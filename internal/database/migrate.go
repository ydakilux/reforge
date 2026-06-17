package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// legacyRunGap is the maximum gap between two consecutive conversions for them
// to be considered part of the same synthesised legacy run. Conversions farther
// apart than this are split into separate runs.
const legacyRunGap = 10 * time.Minute

// migrateSchema brings older databases up to date. It is idempotent: running it
// against an already-current schema is a no-op.
//
// Steps:
//  1. Ensure the `runs` table exists (createTableSQL already does this, but
//     callers may run migrateSchema against an arbitrary DB).
//  2. Add the `run_id` column to `conversions` if missing.
//  3. Backfill `run_id` for rows that have NULL by clustering rows on their
//     `converted_at` timestamp (gap > legacyRunGap starts a new run). All such
//     synthesised runs are tagged with note='legacy'.
func migrateSchema(ctx context.Context, db *sql.DB) error {
	if err := ensureRunIDColumn(ctx, db); err != nil {
		return fmt.Errorf("ensure run_id column: %w", err)
	}
	if err := backfillLegacyRuns(ctx, db); err != nil {
		return fmt.Errorf("backfill legacy runs: %w", err)
	}
	return nil
}

// ensureRunIDColumn adds the run_id column to the conversions table when it is
// missing (older databases created before the runs feature).
func ensureRunIDColumn(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(conversions)`)
	if err != nil {
		return fmt.Errorf("pragma table_info: %w", err)
	}
	defer rows.Close()

	hasRunID := false
	for rows.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notnull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return fmt.Errorf("scan column info: %w", err)
		}
		if name == "run_id" {
			hasRunID = true
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate columns: %w", err)
	}
	if hasRunID {
		// Column already present (fresh DB or previously migrated). Still
		// make sure the supporting index exists — it lives outside
		// createTableSQL because that statement runs before this migration.
		if _, err := db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_conversions_run ON conversions(run_id)`); err != nil {
			return fmt.Errorf("create run_id index: %w", err)
		}
		return nil
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE conversions ADD COLUMN run_id INTEGER`); err != nil {
		return fmt.Errorf("add run_id column: %w", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_conversions_run ON conversions(run_id)`); err != nil {
		return fmt.Errorf("create run_id index: %w", err)
	}
	return nil
}

// backfillLegacyRuns groups orphan conversions (run_id IS NULL) into synthetic
// run rows based on the time gap between consecutive `converted_at` values.
// Rows without a parseable timestamp are bundled into a single catch-all run.
func backfillLegacyRuns(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `
		SELECT rowid, converted_at, original_size, converted_size, error, note
		FROM conversions
		WHERE run_id IS NULL
		ORDER BY converted_at ASC, rowid ASC`)
	if err != nil {
		return fmt.Errorf("query orphans: %w", err)
	}

	type orphan struct {
		rowid         int64
		ts            time.Time
		hasTS         bool
		originalSize  int64
		convertedSize int64
		isError       bool
		isNotBenef    bool
	}

	var orphans []orphan
	for rows.Next() {
		var (
			o         orphan
			tsStr     sql.NullString
			convSize  sql.NullInt64
			errField  sql.NullString
			noteField sql.NullString
			origInt64 int64
		)
		if err := rows.Scan(&o.rowid, &tsStr, &origInt64, &convSize, &errField, &noteField); err != nil {
			rows.Close()
			return fmt.Errorf("scan orphan: %w", err)
		}
		o.originalSize = origInt64
		o.convertedSize = convSize.Int64
		o.isError = errField.Valid && errField.String != ""
		o.isNotBenef = noteField.Valid && noteField.String == "not_beneficial"
		if tsStr.Valid && tsStr.String != "" {
			if t, ok := parseConvertedAt(tsStr.String); ok {
				o.ts = t
				o.hasTS = true
			}
		}
		orphans = append(orphans, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate orphans: %w", err)
	}
	if len(orphans) == 0 {
		return nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	insertRun, err := tx.PrepareContext(ctx, `
		INSERT INTO runs (started_at, ended_at, source_paths, output_drive, encoder,
		                  quality_preset, parallel_jobs, file_count, original_bytes,
		                  converted_bytes, error_count, not_beneficial, note)
		VALUES (?, ?, NULL, '', '', '', 0, ?, ?, ?, ?, ?, 'legacy')`)
	if err != nil {
		return fmt.Errorf("prepare insert run: %w", err)
	}
	defer insertRun.Close()

	updateConv, err := tx.PrepareContext(ctx, `UPDATE conversions SET run_id = ? WHERE rowid = ?`)
	if err != nil {
		return fmt.Errorf("prepare update conv: %w", err)
	}
	defer updateConv.Close()

	// Cluster orphans by time gap. Rows lacking a timestamp all go into a
	// single trailing "undated" cluster.
	type cluster struct {
		members        []orphan
		start, end     time.Time
		hasTS          bool
		fileCount      int
		originalBytes  int64
		convertedBytes int64
		errorCount     int
		notBeneficial  int
	}
	var clusters []cluster
	var current cluster
	flush := func() {
		if len(current.members) == 0 {
			return
		}
		clusters = append(clusters, current)
		current = cluster{}
	}
	for _, o := range orphans {
		if !o.hasTS {
			continue // handled separately below
		}
		if len(current.members) == 0 {
			current.start = o.ts
			current.end = o.ts
			current.hasTS = true
		} else if o.ts.Sub(current.end) > legacyRunGap {
			flush()
			current.start = o.ts
			current.end = o.ts
			current.hasTS = true
		} else {
			current.end = o.ts
		}
		current.members = append(current.members, o)
		current.fileCount++
		current.originalBytes += o.originalSize
		current.convertedBytes += o.convertedSize
		if o.isError {
			current.errorCount++
		}
		if o.isNotBenef {
			current.notBeneficial++
		}
	}
	flush()

	// Undated orphans → one synthetic run with zero timestamps.
	var undated cluster
	for _, o := range orphans {
		if o.hasTS {
			continue
		}
		undated.members = append(undated.members, o)
		undated.fileCount++
		undated.originalBytes += o.originalSize
		undated.convertedBytes += o.convertedSize
		if o.isError {
			undated.errorCount++
		}
		if o.isNotBenef {
			undated.notBeneficial++
		}
	}
	if len(undated.members) > 0 {
		clusters = append(clusters, undated)
	}

	for _, c := range clusters {
		var startedAt, endedAt interface{}
		if c.hasTS {
			startedAt = c.start.UTC().Format(time.RFC3339)
			endedAt = c.end.UTC().Format(time.RFC3339)
		} else {
			// Use the Unix epoch so the row sorts before any real run.
			startedAt = time.Unix(0, 0).UTC().Format(time.RFC3339)
			endedAt = nil
		}
		res, err := insertRun.ExecContext(ctx,
			startedAt, endedAt,
			c.fileCount, c.originalBytes, c.convertedBytes,
			c.errorCount, c.notBeneficial,
		)
		if err != nil {
			return fmt.Errorf("insert legacy run: %w", err)
		}
		runID, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("last insert id: %w", err)
		}
		for _, m := range c.members {
			if _, err := updateConv.ExecContext(ctx, runID, m.rowid); err != nil {
				return fmt.Errorf("attach run_id: %w", err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}
	return nil
}

// parseConvertedAt parses the timestamp formats reforge has written across
// versions (RFC3339 with/without zone, SQLite default "YYYY-MM-DD HH:MM:SS").
func parseConvertedAt(s string) (time.Time, bool) {
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
