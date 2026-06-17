package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// CreateRun inserts a new run row and returns its auto-generated id.
func (s *SQLiteStore) CreateRun(ctx context.Context, info RunInfo) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO runs (started_at, source_paths, output_drive, encoder,
		                  quality_preset, parallel_jobs, note)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		info.StartedAt,
		nullString(strings.Join(info.SourcePaths, "\n")),
		info.OutputDrive,
		info.Encoder,
		info.QualityPreset,
		info.ParallelJobs,
		nullString(info.Note),
	)
	if err != nil {
		return 0, fmt.Errorf("insert run: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("last insert id: %w", err)
	}
	return id, nil
}

// FinalizeRun updates the run row with end time and aggregate counters.
func (s *SQLiteStore) FinalizeRun(ctx context.Context, runID int64, stats RunStats) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE runs
		SET ended_at        = ?,
		    file_count      = ?,
		    original_bytes  = ?,
		    converted_bytes = ?,
		    error_count     = ?,
		    not_beneficial  = ?
		WHERE id = ?`,
		stats.EndedAt,
		stats.FileCount, stats.OriginalBytes, stats.ConvertedBytes,
		stats.ErrorCount, stats.NotBeneficial,
		runID,
	)
	if err != nil {
		return fmt.Errorf("finalize run: %w", err)
	}
	return nil
}

// GetRuns returns the most recent runs ordered by started_at descending. Pass
// limit<=0 to return every run.
//
// For every run we also compute a small per-run breakdown by joining against
// the conversions table. This keeps the Recent-runs UI cheap (one query for
// the whole list rather than N+1 file-listing queries) while still letting it
// distinguish skip-only sessions from real conversion runs.
func (s *SQLiteStore) GetRuns(ctx context.Context, limit int) ([]RunSummary, error) {
	query := `
		SELECT r.id, r.started_at, r.ended_at, r.source_paths, r.output_drive, r.encoder,
		       r.quality_preset, r.parallel_jobs, r.file_count, r.original_bytes,
		       r.converted_bytes, r.error_count, r.not_beneficial, r.note,
		       COALESCE(SUM(CASE WHEN c.note = 'already_hevc' THEN 1 ELSE 0 END), 0)    AS skipped_hevc,
		       COALESCE(SUM(CASE WHEN c.note = 'not_beneficial' THEN 1 ELSE 0 END), 0)  AS not_benef,
		       COALESCE(SUM(CASE WHEN c.error IS NOT NULL AND c.error <> '' THEN 1 ELSE 0 END), 0) AS errored,
		       COALESCE(SUM(CASE WHEN (c.note IS NULL OR c.note = '')
		                          AND (c.error IS NULL OR c.error = '')
		                         THEN 1 ELSE 0 END), 0)                                 AS kept,
		       COALESCE(COUNT(c.rowid), 0)                                              AS attached
		FROM runs r
		LEFT JOIN conversions c ON c.run_id = r.id
		GROUP BY r.id
		ORDER BY r.started_at DESC, r.id DESC`
	var args []interface{}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query runs: %w", err)
	}
	defer rows.Close()

	var results []RunSummary
	for rows.Next() {
		var (
			r        RunSummary
			endedAt  sql.NullString
			srcPaths sql.NullString
			drive    sql.NullString
			enc      sql.NullString
			preset   sql.NullString
			pj       sql.NullInt64
			note     sql.NullString
		)
		if err := rows.Scan(
			&r.ID, &r.StartedAt, &endedAt, &srcPaths, &drive, &enc,
			&preset, &pj, &r.FileCount, &r.OriginalBytes,
			&r.ConvertedBytes, &r.ErrorCount, &r.NotBeneficial, &note,
			&r.SkippedAlreadyHEVC, &r.NotBeneficialFiles, &r.ErroredFiles,
			&r.ConvertedKept, &r.AttachedTotal,
		); err != nil {
			return nil, fmt.Errorf("scan run: %w", err)
		}
		r.EndedAt = endedAt.String
		r.OutputDrive = drive.String
		r.Encoder = enc.String
		r.QualityPreset = preset.String
		r.ParallelJobs = int(pj.Int64)
		r.Note = note.String
		if srcPaths.Valid && srcPaths.String != "" {
			r.SourcePaths = strings.Split(srcPaths.String, "\n")
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate runs: %w", err)
	}
	return results, nil
}

// GetRunFiles returns every conversions row attached to the given run, ordered
// by converted_at ascending so the user sees them in chronological order.
func (s *SQLiteStore) GetRunFiles(ctx context.Context, runID int64) ([]RunFileRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT file_hash, drive_root, source_path, output_path,
		       original_size, converted_size, note, error,
		       source_codec, converted_at
		FROM conversions
		WHERE run_id = ?
		ORDER BY converted_at ASC, rowid ASC`, runID)
	if err != nil {
		return nil, fmt.Errorf("query run files: %w", err)
	}
	defer rows.Close()

	var results []RunFileRecord
	for rows.Next() {
		var (
			rec         RunFileRecord
			sourcePath  sql.NullString
			outputPath  sql.NullString
			convSize    sql.NullInt64
			note        sql.NullString
			errField    sql.NullString
			sourceCodec sql.NullString
			convertedAt sql.NullString
		)
		if err := rows.Scan(
			&rec.FileHash, &rec.DriveRoot, &sourcePath, &outputPath,
			&rec.OriginalSize, &convSize, &note, &errField,
			&sourceCodec, &convertedAt,
		); err != nil {
			return nil, fmt.Errorf("scan run file: %w", err)
		}
		rec.SourcePath = sourcePath.String
		rec.OutputPath = outputPath.String
		rec.ConvertedSize = convSize.Int64
		rec.Note = note.String
		rec.Error = errField.String
		rec.SourceCodec = sourceCodec.String
		rec.ConvertedAt = convertedAt.String
		results = append(results, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate run files: %w", err)
	}
	return results, nil
}
