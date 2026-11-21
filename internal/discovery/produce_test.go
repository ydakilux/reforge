package discovery_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/ydakilux/reforge/internal/database"
	"github.com/ydakilux/reforge/internal/discovery"
	"github.com/ydakilux/reforge/internal/fileutil"
	"github.com/ydakilux/reforge/internal/pipeline"
	"github.com/ydakilux/reforge/internal/types"
)

// writeFakeFFprobe creates a POSIX shell script that mimics ffprobe: it reports
// a single HEVC video stream for -show_streams and a fixed duration otherwise.
func writeFakeFFprobe(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell script as fake ffprobe")
	}
	script := filepath.Join(t.TempDir(), "ffprobe")
	body := `#!/bin/sh
for a in "$@"; do
  if [ "$a" = "-show_streams" ]; then
    echo '{"streams":[{"codec_type":"video","codec_name":"hevc","codec_tag_string":"hvc1","width":1920,"height":1080}]}'
    exit 0
  fi
done
echo '{"format":{"duration":"12.5"}}'
`
	if err := os.WriteFile(script, []byte(body), 0755); err != nil {
		t.Fatal(err)
	}
	return script
}

type produceEnv struct {
	store     *database.SQLiteStore
	file      string
	driveRoot string
	hash      string
	probe     string
	stats     *types.Stats
}

func newProduceEnv(t *testing.T) *produceEnv {
	t.Helper()
	probe := writeFakeFFprobe(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(file, []byte("not really a video"), 0644); err != nil {
		t.Fatal(err)
	}
	store, err := database.NewSQLiteStore(filepath.Join(t.TempDir(), "c.db"), newTestLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	hash, err := fileutil.GetFileHash(file, true)
	if err != nil {
		t.Fatal(err)
	}
	return &produceEnv{
		store: store, file: file, driveRoot: fileutil.GetDriveRoot(file), hash: hash, probe: probe,
		stats: &types.Stats{TouchedDrives: map[string]bool{}},
	}
}

func (e *produceEnv) seed(t *testing.T, rec types.Record) {
	t.Helper()
	rec.OriginalSize = 18
	rec.SourcePath = e.file
	if err := e.store.UpdateRecord(context.Background(), e.driveRoot, e.hash, rec); err != nil {
		t.Fatal(err)
	}
}

// produce runs the real producer against the single test file and returns the
// jobs that reached the pipeline.
func (e *produceEnv) produce(t *testing.T, bypass, forceHevc bool) []types.Job {
	t.Helper()
	pipe := pipeline.NewPipeline(context.Background(), 1, 4)
	var (
		mu   sync.Mutex
		jobs []types.Job
	)
	pipe.Start(func(_ context.Context, job types.Job) pipeline.ConversionResult {
		mu.Lock()
		jobs = append(jobs, job)
		mu.Unlock()
		return pipeline.ConversionResult{Job: job}
	})
	go func() {
		for range pipe.Results() {
		}
	}()

	discovery.Produce([]string{e.file}, map[string]string{e.file: filepath.Dir(e.file)}, pipe, bypass, forceHevc,
		discovery.ProducerConfig{
			Config: &types.Config{UsePartialHash: true, FFprobePath: e.probe},
			DB:     e.store,
			Stats:  e.stats,
			Log:    newTestLogger(),
		})

	mu.Lock()
	defer mu.Unlock()
	return append([]types.Job(nil), jobs...)
}

func (e *produceEnv) record(t *testing.T) *types.Record {
	t.Helper()
	rec, err := e.store.GetRecord(context.Background(), e.driveRoot, e.hash)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

// Without --force-hevc an HEVC file is skipped and remembered as already_hevc.
func TestProduce_HEVCSkippedWithoutForce(t *testing.T) {
	e := newProduceEnv(t)
	if jobs := e.produce(t, false, false); len(jobs) != 0 {
		t.Fatalf("got %d jobs, want 0 (HEVC skipped)", len(jobs))
	}
	if rec := e.record(t); rec == nil || rec.Note != "already_hevc" {
		t.Fatalf("record = %+v, want note already_hevc", rec)
	}
	if e.stats.FilesSkipped != 1 {
		t.Errorf("FilesSkipped = %d, want 1", e.stats.FilesSkipped)
	}
}

// --force-hevc converts HEVC files that have never been seen.
func TestProduce_ForceHEVCConvertsNewHEVCFile(t *testing.T) {
	e := newProduceEnv(t)
	if jobs := e.produce(t, false, true); len(jobs) != 1 {
		t.Fatalf("got %d jobs, want 1 (forced)", len(jobs))
	}
}

// Regression: an already_hevc record left by an earlier run used to veto a
// later --force-hevc run unless --bypass was also given.
func TestProduce_ForceHEVCOverridesPriorAlreadyHEVCRecord(t *testing.T) {
	e := newProduceEnv(t)
	e.seed(t, types.Record{Note: "already_hevc", ConvertedSize: 18})

	if jobs := e.produce(t, false, false); len(jobs) != 0 {
		t.Fatalf("without force: got %d jobs, want 0 (prior already_hevc record)", len(jobs))
	}

	e2 := newProduceEnv(t)
	e2.seed(t, types.Record{Note: "already_hevc", ConvertedSize: 18})
	if jobs := e2.produce(t, false, true); len(jobs) != 1 {
		t.Fatalf("with force: got %d jobs, want 1 (force-hevc must override the stale record)", len(jobs))
	}
}

// --force-hevc must NOT re-convert files that were genuinely converted or found
// not beneficial; only the "already HEVC" skip is overridden.
func TestProduce_ForceHEVCStillSkipsConvertedAndNotBeneficial(t *testing.T) {
	cases := map[string]types.Record{
		"converted":      {Output: "/out/movie.mkv", ConvertedSize: 9},
		"not_beneficial": {Note: "not_beneficial", ConvertedSize: 30},
	}
	for name, rec := range cases {
		t.Run(name, func(t *testing.T) {
			e := newProduceEnv(t)
			e.seed(t, rec)
			if jobs := e.produce(t, false, true); len(jobs) != 0 {
				t.Fatalf("got %d jobs, want 0", len(jobs))
			}
		})
	}
}

// --bypass ignores every prior record, including already_hevc.
func TestProduce_BypassIgnoresRecords(t *testing.T) {
	e := newProduceEnv(t)
	e.seed(t, types.Record{Output: "/out/movie.mkv", ConvertedSize: 9})
	// bypass alone still skips the HEVC source itself (not forced) ...
	if jobs := e.produce(t, true, false); len(jobs) != 0 {
		t.Fatalf("bypass without force: got %d jobs, want 0 (source is HEVC)", len(jobs))
	}
	// ... but bypass + force converts despite the record.
	e2 := newProduceEnv(t)
	e2.seed(t, types.Record{Output: "/out/movie.mkv", ConvertedSize: 9})
	if jobs := e2.produce(t, true, true); len(jobs) != 1 {
		t.Fatalf("bypass+force: got %d jobs, want 1", len(jobs))
	}
}
