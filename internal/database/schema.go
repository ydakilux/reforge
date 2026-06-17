package database

// createTableSQL creates the base schema. Older databases may not have all
// tables/columns; migrateSchema handles upgrades from earlier versions.
const createTableSQL = `
CREATE TABLE IF NOT EXISTS conversions (
    file_hash               TEXT    NOT NULL,
    drive_root              TEXT    NOT NULL,
    source_path             TEXT,
    original_size           INTEGER NOT NULL,
    converted_size          INTEGER,
    output_path             TEXT,
    note                    TEXT,
    error                   TEXT,
    source_codec            TEXT,
    source_container        TEXT,
    width                   INTEGER,
    height                  INTEGER,
    duration_secs           REAL,
    converted_at            TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    conversion_duration_secs REAL,
    run_id                  INTEGER,
    PRIMARY KEY (file_hash, drive_root)
);
CREATE INDEX IF NOT EXISTS idx_conversions_drive ON conversions(drive_root);
CREATE INDEX IF NOT EXISTS idx_conversions_error ON conversions(error) WHERE error IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_conversions_note ON conversions(note) WHERE note IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_conversions_converted_at ON conversions(converted_at);
-- idx_conversions_run is created inside migrateSchema, *after* the run_id
-- column has been ensured on legacy databases. Putting it here would fail
-- with "no such column: run_id" on existing DBs created before the runs
-- feature, because CREATE TABLE IF NOT EXISTS is a no-op for them.

CREATE TABLE IF NOT EXISTS runs (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    started_at        TIMESTAMP NOT NULL,
    ended_at          TIMESTAMP,
    source_paths      TEXT,                 -- newline-separated list of input paths
    output_drive      TEXT,                 -- "" means same-drive
    encoder           TEXT,
    quality_preset    TEXT,
    parallel_jobs     INTEGER,
    file_count        INTEGER NOT NULL DEFAULT 0,
    original_bytes    INTEGER NOT NULL DEFAULT 0,
    converted_bytes   INTEGER NOT NULL DEFAULT 0,
    error_count       INTEGER NOT NULL DEFAULT 0,
    not_beneficial    INTEGER NOT NULL DEFAULT 0,
    note              TEXT                  -- e.g. "legacy" for synthesised runs
);
CREATE INDEX IF NOT EXISTS idx_runs_started_at ON runs(started_at);
`
