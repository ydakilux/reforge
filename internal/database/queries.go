package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// GetStats returns aggregate conversion statistics, optionally filtered by drive root.
// Pass driveRoot="" for all drives.
func (s *SQLiteStore) GetStats(ctx context.Context, driveRoot string) (*Stats, error) {
	query := `
		SELECT COUNT(*),
		       COALESCE(SUM(original_size), 0),
		       COALESCE(SUM(converted_size), 0),
		       COUNT(CASE WHEN error IS NOT NULL AND error != '' THEN 1 END),
		       COUNT(CASE WHEN note = 'not_beneficial' THEN 1 END),
		       COUNT(CASE WHEN note = 'already_hevc' THEN 1 END)
		FROM conversions`

	var args []interface{}
	if driveRoot != "" {
		query += ` WHERE drive_root = ?`
		args = append(args, driveRoot)
	}

	var st Stats
	err := s.db.QueryRowContext(ctx, query, args...).Scan(
		&st.TotalFiles,
		&st.TotalOriginal,
		&st.TotalConverted,
		&st.ErrorCount,
		&st.NotBeneficial,
		&st.AlreadyHEVC,
	)
	if err != nil {
		return nil, fmt.Errorf("get stats: %w", err)
	}

	st.SuccessCount = st.TotalFiles - st.ErrorCount - st.NotBeneficial - st.AlreadyHEVC
	return &st, nil
}

// GetErrors returns records that failed conversion.
// Pass driveRoot="" for all drives, pathFilter="" for no path filter.
// pathFilter uses SQL LIKE syntax (e.g. "%Movies%").
func (s *SQLiteStore) GetErrors(ctx context.Context, driveRoot, pathFilter string) ([]ErrorRecord, error) {
	query := `
		SELECT file_hash, drive_root, source_path, original_size, error, converted_at
		FROM conversions
		WHERE error IS NOT NULL AND error != ''`

	var args []interface{}
	if driveRoot != "" {
		query += ` AND drive_root = ?`
		args = append(args, driveRoot)
	}
	if pathFilter != "" {
		query += ` AND source_path LIKE ?`
		args = append(args, pathFilter)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("get errors: %w", err)
	}
	defer rows.Close()

	var results []ErrorRecord
	for rows.Next() {
		var (
			rec         ErrorRecord
			sourcePath  sql.NullString
			errField    sql.NullString
			convertedAt sql.NullString
		)
		if err := rows.Scan(
			&rec.FileHash, &rec.DriveRoot, &sourcePath,
			&rec.OriginalSize, &errField, &convertedAt,
		); err != nil {
			return nil, fmt.Errorf("scan error record: %w", err)
		}
		rec.SourcePath = sourcePath.String
		rec.Error = errField.String
		rec.ConvertedAt = convertedAt.String
		results = append(results, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate error records: %w", err)
	}
	return results, nil
}

// GetNotBeneficial returns records where conversion was not beneficial.
// Pass driveRoot="" for all drives.
func (s *SQLiteStore) GetNotBeneficial(ctx context.Context, driveRoot string) ([]NotBeneficialRecord, error) {
	query := `
		SELECT file_hash, drive_root, source_path, original_size, converted_size
		FROM conversions
		WHERE note = 'not_beneficial'`

	var args []interface{}
	if driveRoot != "" {
		query += ` AND drive_root = ?`
		args = append(args, driveRoot)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("get not beneficial: %w", err)
	}
	defer rows.Close()

	var results []NotBeneficialRecord
	for rows.Next() {
		var (
			rec           NotBeneficialRecord
			sourcePath    sql.NullString
			convertedSize sql.NullInt64
		)
		if err := rows.Scan(
			&rec.FileHash, &rec.DriveRoot, &sourcePath,
			&rec.OriginalSize, &convertedSize,
		); err != nil {
			return nil, fmt.Errorf("scan not beneficial record: %w", err)
		}
		rec.SourcePath = sourcePath.String
		rec.ConvertedSize = convertedSize.Int64
		results = append(results, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate not beneficial records: %w", err)
	}
	return results, nil
}

// GetRecent returns the most recent conversion records, ordered by converted_at descending.
func (s *SQLiteStore) GetRecent(ctx context.Context, limit int) ([]RecentRecord, error) {
	query := `
		SELECT file_hash, drive_root, source_path, original_size, converted_size,
		       output_path, note, error, source_codec, converted_at
		FROM conversions
		ORDER BY converted_at DESC
		LIMIT ?`

	rows, err := s.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("get recent: %w", err)
	}
	defer rows.Close()

	var results []RecentRecord
	for rows.Next() {
		var (
			rec           RecentRecord
			sourcePath    sql.NullString
			convertedSize sql.NullInt64
			outputPath    sql.NullString
			note          sql.NullString
			errField      sql.NullString
			sourceCodec   sql.NullString
			convertedAt   sql.NullString
		)
		if err := rows.Scan(
			&rec.FileHash, &rec.DriveRoot, &sourcePath,
			&rec.OriginalSize, &convertedSize, &outputPath,
			&note, &errField, &sourceCodec, &convertedAt,
		); err != nil {
			return nil, fmt.Errorf("scan recent record: %w", err)
		}
		rec.SourcePath = sourcePath.String
		rec.ConvertedSize = convertedSize.Int64
		rec.OutputPath = outputPath.String
		rec.Note = note.String
		rec.Error = errField.String
		rec.SourceCodec = sourceCodec.String
		rec.ConvertedAt = convertedAt.String
		results = append(results, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recent records: %w", err)
	}
	return results, nil
}

// GetFormatBreakdown returns conversion statistics grouped by source codec and container.
// Pass driveRoot="" for all drives.
func (s *SQLiteStore) GetFormatBreakdown(ctx context.Context, driveRoot string) ([]FormatStat, error) {
	query := `
		SELECT source_codec, source_container, COUNT(*),
		       COALESCE(SUM(original_size), 0),
		       COALESCE(SUM(converted_size), 0)
		FROM conversions`

	var args []interface{}
	if driveRoot != "" {
		query += ` WHERE drive_root = ?`
		args = append(args, driveRoot)
	}
	query += ` GROUP BY source_codec, source_container`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("get format breakdown: %w", err)
	}
	defer rows.Close()

	var results []FormatStat
	for rows.Next() {
		var (
			fs              FormatStat
			sourceCodec     sql.NullString
			sourceContainer sql.NullString
		)
		if err := rows.Scan(
			&sourceCodec, &sourceContainer, &fs.Count,
			&fs.TotalOriginal, &fs.TotalConverted,
		); err != nil {
			return nil, fmt.Errorf("scan format stat: %w", err)
		}
		fs.SourceCodec = sourceCodec.String
		fs.SourceContainer = sourceContainer.String
		results = append(results, fs)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate format stats: %w", err)
	}
	return results, nil
}

// GetSpaceSaved returns total space saved for a given time period.
// period: "week" (7 days), "month" (30 days), "total" (all time).
func (s *SQLiteStore) GetSpaceSaved(ctx context.Context, period string) (*SpaceSavedResult, error) {
	var conditions []string
	conditions = append(conditions, `(error IS NULL OR error = '')`)
	conditions = append(conditions, `(note IS NULL OR note = '')`)

	var args []interface{}
	// converted_at is stored as RFC3339 UTC ("2006-01-02T15:04:05Z"). The bound
	// must use the same format: datetime() yields "YYYY-MM-DD HH:MM:SS", and as
	// strings 'T' > ' ', which skewed the cutoff by up to a day.
	switch period {
	case "week":
		conditions = append(conditions, `converted_at >= strftime('%Y-%m-%dT%H:%M:%SZ', 'now', '-7 days')`)
	case "month":
		conditions = append(conditions, `converted_at >= strftime('%Y-%m-%dT%H:%M:%SZ', 'now', '-30 days')`)
	case "total":
		// no time filter
	default:
		return nil, fmt.Errorf("invalid period %q: must be week, month, or total", period)
	}

	query := `
		SELECT COUNT(*),
		       COALESCE(SUM(original_size - converted_size), 0)
		FROM conversions
		WHERE ` + strings.Join(conditions, " AND ")

	var result SpaceSavedResult
	result.Period = period
	err := s.db.QueryRowContext(ctx, query, args...).Scan(
		&result.FileCount,
		&result.BytesSaved,
	)
	if err != nil {
		return nil, fmt.Errorf("get space saved: %w", err)
	}
	return &result, nil
}

func (s *SQLiteStore) GetConversionTimeline(ctx context.Context) ([]TimelinePoint, error) {
	query := `
		SELECT DATE(converted_at) AS day,
		       drive_root,
		       COUNT(*),
		       COALESCE(SUM(original_size), 0),
		       COALESCE(SUM(converted_size), 0),
		       COALESCE(SUM(original_size - converted_size), 0)
		FROM conversions
		WHERE converted_at IS NOT NULL
		  AND (error IS NULL OR error = '')
		  AND (note IS NULL OR note = '')
		GROUP BY day, drive_root
		ORDER BY day ASC`

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("get conversion timeline: %w", err)
	}
	defer rows.Close()

	var results []TimelinePoint
	for rows.Next() {
		var (
			tp        TimelinePoint
			day       sql.NullString
			driveRoot sql.NullString
		)
		if err := rows.Scan(&day, &driveRoot, &tp.Count, &tp.TotalOriginal, &tp.TotalConverted, &tp.BytesSaved); err != nil {
			return nil, fmt.Errorf("scan timeline point: %w", err)
		}
		tp.Date = day.String
		tp.DriveRoot = driveRoot.String
		results = append(results, tp)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate timeline points: %w", err)
	}
	return results, nil
}

func (s *SQLiteStore) GetDriveRoots(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT drive_root FROM conversions ORDER BY drive_root`)
	if err != nil {
		return nil, fmt.Errorf("get drive roots: %w", err)
	}
	defer rows.Close()

	var results []string
	for rows.Next() {
		var dr string
		if err := rows.Scan(&dr); err != nil {
			return nil, fmt.Errorf("scan drive root: %w", err)
		}
		results = append(results, dr)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate drive roots: %w", err)
	}
	return results, nil
}
