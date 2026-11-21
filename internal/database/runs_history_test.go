package database

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/sirupsen/logrus"

	"github.com/ydakilux/reforge/internal/types"
)

func newRun(t *testing.T, s *SQLiteStore, started string) int64 {
	t.Helper()
	id, err := s.CreateRun(context.Background(), RunInfo{StartedAt: started})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// Re-processing a file in a later run must not remove it from the earlier run.
func TestRunHistory_ReprocessingKeepsOldRunFiles(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()
	runA := newRun(t, s, "2026-01-01T10:00:00Z")
	runB := newRun(t, s, "2026-01-02T10:00:00Z")

	recA := types.Record{OriginalSize: 1000, ConvertedSize: 400, Output: `D:\REFORGED\a.mp4`, SourcePath: `D:\v\a.mkv`, ConvertedAt: "2026-01-01T10:01:00Z", RunID: runA}
	if err := s.UpdateRecord(ctx, `D:\`, "h1", recA); err != nil {
		t.Fatal(err)
	}
	recB := recA
	recB.RunID, recB.ConvertedAt = runB, "2026-01-02T10:01:00Z"
	if err := s.UpdateRecord(ctx, `D:\`, "h1", recB); err != nil {
		t.Fatal(err)
	}

	for _, id := range []int64{runA, runB} {
		files, err := s.GetRunFiles(ctx, id)
		if err != nil || len(files) != 1 {
			t.Fatalf("run %d files = %v err=%v, want exactly 1", id, files, err)
		}
	}
	runs, err := s.GetRuns(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range runs {
		if r.AttachedTotal != 1 || r.ConvertedKept != 1 {
			t.Errorf("run %d: attached=%d kept=%d, want 1/1", r.ID, r.AttachedTotal, r.ConvertedKept)
		}
	}
}

// A failed re-attempt keeps the conversions record (and its output link) but is
// still visible in the failing run's own history.
func TestRunHistory_FailureRecordedInRunButNotInConversions(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()
	runA := newRun(t, s, "2026-01-01T10:00:00Z")
	runB := newRun(t, s, "2026-01-02T10:00:00Z")

	ok := types.Record{OriginalSize: 1000, ConvertedSize: 400, Output: `D:\REFORGED\a.mp4`, SourcePath: `D:\v\a.mkv`, RunID: runA, ConvertedAt: "2026-01-01T10:01:00Z"}
	if err := s.UpdateRecord(ctx, `D:\`, "h1", ok); err != nil {
		t.Fatal(err)
	}
	fail := types.Record{OriginalSize: 1000, Error: "rc_1", SourcePath: `D:\v\a.mkv`, RunID: runB, ConvertedAt: "2026-01-02T10:01:00Z"}
	if err := s.UpdateRecord(ctx, `D:\`, "h1", fail); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetRecord(ctx, `D:\`, "h1")
	if err != nil || got == nil || got.Output != ok.Output || got.Error != "" {
		t.Fatalf("conversions record clobbered: %+v err=%v", got, err)
	}
	files, err := s.GetRunFiles(ctx, runB)
	if err != nil || len(files) != 1 || files[0].Error != "rc_1" {
		t.Fatalf("run B history = %+v err=%v, want the failure", files, err)
	}
}

// An existing database (conversions rows with run_id, no run_files table) is
// seeded once when opened by the new version.
func TestRunHistory_MigrationBackfillsExistingRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.db")
	l := logrus.New()
	s, err := NewSQLiteStore(path, l)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	runID := newRun(t, s, "2026-01-01T10:00:00Z")
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO conversions (file_hash, drive_root, source_path, original_size, converted_size, output_path, converted_at, run_id)
		VALUES ('h1', 'D:\', 'D:\v\a.mkv', 1000, 400, 'D:\REFORGED\a.mp4', '2026-01-01T10:01:00Z', ?)`, runID); err != nil {
		t.Fatal(err)
	}
	// Simulate the pre-run_files schema.
	if _, err := s.db.ExecContext(ctx, `DROP TABLE run_files`); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s2, err := NewSQLiteStore(path, l)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	files, err := s2.GetRunFiles(ctx, runID)
	if err != nil || len(files) != 1 || files[0].FileHash != "h1" {
		t.Fatalf("backfill failed: %+v err=%v", files, err)
	}
}
