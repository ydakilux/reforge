package database

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/sirupsen/logrus"
	_ "modernc.org/sqlite"

	"github.com/ydakilux/reforge/internal/types"
)

var _ Store = (*SQLiteStore)(nil)

type SQLiteStore struct {
	db     *sql.DB
	logger *logrus.Logger
}

func NewSQLiteStore(dbPath string, logger *logrus.Logger) (*SQLiteStore, error) {
	dsn := dbPath + "?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	db.SetMaxOpenConns(1)

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, createTableSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}
	if err := migrateSchema(ctx, db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate schema: %w", err)
	}

	return &SQLiteStore{db: db, logger: logger}, nil
}

func (s *SQLiteStore) GetRecord(ctx context.Context, driveRoot, fileHash string) (*types.Record, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT source_path, original_size, converted_size, output_path, note, error,
		       source_codec, source_container, width, height, duration_secs,
		       converted_at, conversion_duration_secs
		FROM conversions
		WHERE file_hash = ? AND drive_root = ?`,
		fileHash, driveRoot,
	)

	var (
		sourcePath      sql.NullString
		originalSize    int64
		convertedSize   sql.NullInt64
		outputPath      sql.NullString
		note            sql.NullString
		errField        sql.NullString
		sourceCodec     sql.NullString
		sourceContainer sql.NullString
		width           sql.NullInt64
		height          sql.NullInt64
		durationSecs    sql.NullFloat64
		convertedAt     sql.NullString
		convDurSecs     sql.NullFloat64
	)

	if err := row.Scan(
		&sourcePath, &originalSize, &convertedSize, &outputPath, &note, &errField,
		&sourceCodec, &sourceContainer, &width, &height, &durationSecs,
		&convertedAt, &convDurSecs,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("scan record: %w", err)
	}

	rec := &types.Record{
		OriginalSize:           originalSize,
		ConvertedSize:          convertedSize.Int64,
		Output:                 outputPath.String,
		Note:                   note.String,
		Error:                  errField.String,
		SourceCodec:            sourceCodec.String,
		SourceContainer:        sourceContainer.String,
		SourcePath:             sourcePath.String,
		Width:                  int(width.Int64),
		Height:                 int(height.Int64),
		DurationSecs:           durationSecs.Float64,
		ConvertedAt:            convertedAt.String,
		ConversionDurationSecs: convDurSecs.Float64,
	}
	return rec, nil
}

func (s *SQLiteStore) UpdateRecord(ctx context.Context, driveRoot, fileHash string, rec types.Record) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO conversions
			(file_hash, drive_root, source_path, original_size, converted_size,
			 output_path, note, error, source_codec, source_container,
			 width, height, duration_secs, converted_at, conversion_duration_secs, run_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		fileHash, driveRoot, nullString(rec.SourcePath), rec.OriginalSize, nullInt64(rec.ConvertedSize),
		nullString(rec.Output), nullString(rec.Note), nullString(rec.Error),
		nullString(rec.SourceCodec), nullString(rec.SourceContainer),
		nullInt64(int64(rec.Width)), nullInt64(int64(rec.Height)), nullFloat64(rec.DurationSecs),
		nullString(rec.ConvertedAt), nullFloat64(rec.ConversionDurationSecs),
		nullInt64(rec.RunID),
	)
	if err != nil {
		return fmt.Errorf("upsert record: %w", err)
	}
	return nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func nullInt64(n int64) sql.NullInt64 {
	if n == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: n, Valid: true}
}

func nullFloat64(f float64) sql.NullFloat64 {
	if f == 0 {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: f, Valid: true}
}
