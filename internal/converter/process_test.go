package converter

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/ydakilux/reforge/internal/database"
	"github.com/ydakilux/reforge/internal/encoder"
	"github.com/ydakilux/reforge/internal/fallback"
	"github.com/ydakilux/reforge/internal/pipeline"
	"github.com/ydakilux/reforge/internal/tui"
	"github.com/ydakilux/reforge/internal/types"
)

// ── harness ──────────────────────────────────────────────────────────────────

type procEnv struct {
	c         *Converter
	store     *database.SQLiteStore
	driveRoot string // also the output root, so every artefact stays in a temp dir
	srcDir    string
	marker    string // created by the fake ffmpeg once it is running
	log       string // one line appended per fake ffmpeg invocation
}

// newProcEnv builds a Converter wired to a fake ffmpeg shell script. body is
// the script text; it may use $MARKER and $LOG, and "$last" (the output path).
func newProcEnv(t *testing.T, encoderName, body string) *procEnv {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell script as fake ffmpeg")
	}
	root := t.TempDir()
	env := &procEnv{
		driveRoot: filepath.Join(root, "drive"),
		srcDir:    filepath.Join(root, "src"),
		marker:    filepath.Join(root, "started"),
		log:       filepath.Join(root, "invocations.log"),
	}
	for _, d := range []string{env.driveRoot, env.srcDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	script := filepath.Join(root, "ffmpeg")
	full := fmt.Sprintf("#!/bin/sh\nMARKER=%q\nLOG=%q\nfor a; do last=$a; done\n%s\n", env.marker, env.log, body)
	if err := os.WriteFile(script, []byte(full), 0755); err != nil {
		t.Fatal(err)
	}

	log := logrus.New()
	log.SetOutput(io.Discard)

	reg := encoder.NewRegistry()
	reg.Register(encoder.NewLibx265Encoder())
	reg.Register(encoder.NewNvencEncoder())
	sel, ok := reg.Get(encoderName)
	if !ok {
		t.Fatalf("unknown encoder %q", encoderName)
	}

	store, err := database.NewSQLiteStore(filepath.Join(root, "c.db"), log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	env.store = store

	env.c = &Converter{
		Config:          &types.Config{FFmpegPath: script, VideoEncoder: encoderName, QualityPreset: "balanced"},
		ExecDir:         root,
		SelectedEncoder: sel,
		EncoderRegistry: reg,
		FallbackManager: fallback.NewFallbackManager(false, strings.NewReader(""), log),
		DB:              store,
		UI:              tui.NewPlain(io.Discard),
		Stats:           &types.Stats{TouchedDrives: map[string]bool{}},
		Log:             log,
	}
	return env
}

func (e *procEnv) job(name, hash string) types.Job {
	return types.Job{
		FilePath: filepath.Join(e.srcDir, name), BaseDir: e.srcDir, DriveRoot: e.driveRoot,
		FileHash: hash, OriginalSize: 1000, FileNumber: 1, TotalFiles: 1, Width: 1920, Height: 1080,
	}
}

func (e *procEnv) errored() int {
	e.c.Stats.Mu.Lock()
	defer e.c.Stats.Mu.Unlock()
	return e.c.Stats.FilesErrored
}

func (e *procEnv) record(t *testing.T, hash string) *types.Record {
	t.Helper()
	rec, err := e.store.GetRecord(context.Background(), e.driveRoot, hash)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func (e *procEnv) invocations(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(e.log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

// leftoverTemp lists any __tmp__ files still present under the output root.
func (e *procEnv) leftoverTemp(t *testing.T) []string {
	t.Helper()
	var found []string
	filepath.Walk(e.driveRoot, func(p string, info os.FileInfo, err error) error { //nolint:errcheck
		if err == nil && !info.IsDir() && strings.HasPrefix(info.Name(), TempFilePrefix) {
			found = append(found, p)
		}
		return nil
	})
	return found
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

// runAndCancel starts Process, waits until the fake ffmpeg is running, cancels
// the context and requires Process to return promptly.
func (e *procEnv) runAndCancel(t *testing.T, job types.Job) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		e.c.Process(ctx, job, false)
		close(done)
	}()
	waitForFile(t, e.marker)
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Process did not return after cancellation")
	}
}

const hashA = "aaaaaaaa11111111"
const hashB = "bbbbbbbb22222222"

// ── cancellation ─────────────────────────────────────────────────────────────

// A user-requested stop kills FFmpeg; that is not a conversion failure.
func TestProcess_CancelIsNotAnError(t *testing.T) {
	env := newProcEnv(t, "libx265", `echo partial > "$last"
echo started > "$MARKER"
exec sleep 30`)

	env.runAndCancel(t, env.job("a.mkv", hashA))

	if n := env.errored(); n != 0 {
		t.Errorf("FilesErrored = %d, want 0 for a cancelled job", n)
	}
	if rec := env.record(t, hashA); rec != nil {
		t.Errorf("a record was written for a cancelled job: %+v", rec)
	}
	if left := env.leftoverTemp(t); len(left) != 0 {
		t.Errorf("temp files left behind: %v", left)
	}
	env.c.Stats.Mu.Lock()
	processed := env.c.Stats.FilesProcessed
	env.c.Stats.Mu.Unlock()
	if processed != 0 {
		t.Errorf("FilesProcessed = %d, want 0", processed)
	}
}

// Control: a genuine FFmpeg failure is still recorded and counted.
func TestProcess_RealFailureStillRecorded(t *testing.T) {
	env := newProcEnv(t, "libx265", `echo partial > "$last"
exit 3`)

	env.c.Process(context.Background(), env.job("a.mkv", hashA), false)

	if n := env.errored(); n != 1 {
		t.Errorf("FilesErrored = %d, want 1", n)
	}
	rec := env.record(t, hashA)
	if rec == nil || rec.Error != "rc_3" {
		t.Fatalf("record = %+v, want error rc_3", rec)
	}
	if left := env.leftoverTemp(t); len(left) != 0 {
		t.Errorf("temp files left behind: %v", left)
	}
}

// Cancelling while FFmpeg is reporting a GPU error must NOT trigger the
// GPU->CPU fallback (no second encode, no sticky CPU decision).
func TestProcess_CancelDoesNotTriggerGPUFallback(t *testing.T) {
	env := newProcEnv(t, "hevc_nvenc", `echo run >> "$LOG"
echo "OpenEncodeSessionEx failed" >&2
echo started > "$MARKER"
exec sleep 30`)

	env.runAndCancel(t, env.job("a.mkv", hashA))

	if got := env.invocations(t); len(got) != 1 {
		t.Errorf("ffmpeg ran %d times, want 1 (no CPU retry after cancel): %v", len(got), got)
	}
	if env.c.FallbackManager.CPUOnly() {
		t.Error("a cancelled job must not switch the run to CPU-only")
	}
	if n := env.errored(); n != 0 {
		t.Errorf("FilesErrored = %d, want 0", n)
	}
	if rec := env.record(t, hashA); rec != nil {
		t.Errorf("unexpected record for cancelled job: %+v", rec)
	}
}

// Control for the test above, and coverage of the sticky fallback: without a
// cancel the GPU error falls back to CPU once, and later files go straight to CPU.
func TestProcess_GPUFailureFallsBackOnceThenStaysOnCPU(t *testing.T) {
	env := newProcEnv(t, "hevc_nvenc", `echo "$*" >> "$LOG"
case "$*" in
  *hevc_nvenc*) echo "OpenEncodeSessionEx failed" >&2; exit 1;;
esac
echo smalldata > "$last"
exit 0`)

	env.c.Process(context.Background(), env.job("a.mkv", hashA), false)

	runs := env.invocations(t)
	if len(runs) != 2 || !strings.Contains(runs[0], "hevc_nvenc") || !strings.Contains(runs[1], "libx265") {
		t.Fatalf("first file: invocations = %v, want nvenc then libx265", runs)
	}
	if !env.c.FallbackManager.CPUOnly() {
		t.Fatal("CPUOnly should be set after the fallback")
	}
	if rec := env.record(t, hashA); rec == nil || rec.Output == "" {
		t.Fatalf("first file not converted via CPU fallback: %+v", rec)
	}

	env.c.Process(context.Background(), env.job("b.mkv", hashB), false)
	runs = env.invocations(t)
	if len(runs) != 3 || strings.Contains(runs[2], "hevc_nvenc") || !strings.Contains(runs[2], "libx265") {
		t.Fatalf("second file: invocations = %v, want a single libx265 run (no GPU retry)", runs)
	}
}

// Cancelling while queued for a GPU slot returns without running FFmpeg or
// leaving any record.
func TestProcess_CancelWhileWaitingForGPUSlot(t *testing.T) {
	env := newProcEnv(t, "hevc_nvenc", `echo run >> "$LOG"
exit 0`)
	slots := pipeline.NewGPUSlots([]int{0}, 1)
	env.c.GPUSlots = slots

	_, release, err := slots.Acquire(context.Background(), 0) // occupy the only slot
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() {
		env.c.Process(ctx, env.job("a.mkv", hashA), false)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Process did not return while waiting for a GPU slot")
	}

	if got := env.invocations(t); len(got) != 0 {
		t.Errorf("ffmpeg should never start without a slot, ran %d times", len(got))
	}
	if n := env.errored(); n != 0 {
		t.Errorf("FilesErrored = %d, want 0", n)
	}
	if rec := env.record(t, hashA); rec != nil {
		t.Errorf("unexpected record: %+v", rec)
	}
}
