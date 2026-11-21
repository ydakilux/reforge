package fallback

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/ydakilux/reforge/internal/encoder"

	"github.com/sirupsen/logrus"
)

type mockEncoder struct {
	isGPU bool
	msg   string
}

func (m *mockEncoder) Name() string                              { return "mock_encoder" }
func (m *mockEncoder) QualityArgs(preset string, w int) []string { return nil }
func (m *mockEncoder) DeviceArgs(gpuIndex int) []string          { return nil }
func (m *mockEncoder) IsAvailable(ffmpegPath string) bool        { return true }
func (m *mockEncoder) ParseError(stderr string) (bool, string) {
	return m.isGPU, m.msg
}

type mockJob struct {
	name string
}

func (j *mockJob) String() string { return j.name }

var _ encoder.Encoder = (*mockEncoder)(nil)

func newTestLogger() *logrus.Logger {
	l := logrus.New()
	l.SetOutput(&bytes.Buffer{})
	l.SetLevel(logrus.DebugLevel)
	return l
}

func TestFallbackInteractiveAccept(t *testing.T) {
	stdin := strings.NewReader("y\n")
	fm := NewFallbackManager(true, stdin, newTestLogger())

	enc := &mockEncoder{isGPU: true, msg: "No capable devices found"}
	job := &mockJob{name: "video.mp4"}

	shouldFallback, err := fm.HandleGPUError("error: No capable devices found", enc, job)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !shouldFallback {
		t.Error("expected shouldFallback=true when user accepts with 'y'")
	}
}

func TestFallbackNonInteractive(t *testing.T) {
	// Non-interactive should not read stdin at all; pass nil-like reader.
	fm := NewFallbackManager(false, strings.NewReader(""), newTestLogger())

	enc := &mockEncoder{isGPU: true, msg: "OpenEncodeSessionEx failed"}
	job := &mockJob{name: "big_video.mkv"}

	shouldFallback, err := fm.HandleGPUError("error: OpenEncodeSessionEx failed", enc, job)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !shouldFallback {
		t.Error("expected shouldFallback=true for non-interactive GPU error")
	}
}

func TestFallbackNonGPUError(t *testing.T) {
	stdin := strings.NewReader("y\n") // Should never be read
	fm := NewFallbackManager(true, stdin, newTestLogger())

	enc := &mockEncoder{isGPU: false, msg: ""}
	job := &mockJob{name: "file.avi"}

	shouldFallback, err := fm.HandleGPUError("some random ffmpeg error", enc, job)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if shouldFallback {
		t.Error("expected shouldFallback=false for non-GPU error")
	}
}

func TestFallbackUserDeclines(t *testing.T) {
	stdin := strings.NewReader("n\n")
	fm := NewFallbackManager(true, stdin, newTestLogger())

	enc := &mockEncoder{isGPU: true, msg: "InitializeEncoder failed"}
	job := &mockJob{name: "clip.mp4"}

	shouldFallback, err := fm.HandleGPUError("error: InitializeEncoder failed", enc, job)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if shouldFallback {
		t.Error("expected shouldFallback=false when user declines with 'n'")
	}
}

func TestFallbackEmptyInput(t *testing.T) {
	stdin := strings.NewReader("\n") // Just pressing Enter → default=yes
	fm := NewFallbackManager(true, stdin, newTestLogger())

	enc := &mockEncoder{isGPU: true, msg: "Encoder creation error"}
	job := &mockJob{name: "movie.mp4"}

	shouldFallback, err := fm.HandleGPUError("error: Encoder creation error", enc, job)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !shouldFallback {
		t.Error("expected shouldFallback=true when user presses Enter (default=yes)")
	}
}

func TestFallbackThreadSafety(t *testing.T) {
	// Verify that concurrent HandleGPUError calls don't race.
	// Non-interactive mode avoids stdin contention.
	fm := NewFallbackManager(false, strings.NewReader(""), newTestLogger())

	enc := &mockEncoder{isGPU: true, msg: "GPU error"}

	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		go func(idx int) {
			job := &mockJob{name: fmt.Sprintf("vid_%d.mp4", idx)}
			shouldFallback, err := fm.HandleGPUError("gpu failure", enc, job)
			if err != nil {
				errs <- fmt.Errorf("goroutine %d: %w", idx, err)
				return
			}
			if !shouldFallback {
				errs <- fmt.Errorf("goroutine %d: expected shouldFallback=true", idx)
				return
			}
			errs <- nil
		}(i)
	}

	for i := 0; i < 10; i++ {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}
}

// The first GPU failure settles the decision for the whole run: later files
// must not re-prompt the user.
func TestFallbackInteractiveDecisionIsRemembered(t *testing.T) {
	// Only ONE answer is available; a second prompt would hit EOF (which also
	// returns true), so use "n" to make a re-prompt observable via the result.
	fm := NewFallbackManager(true, strings.NewReader("n\n"), newTestLogger())
	enc := &mockEncoder{isGPU: true, msg: "gpu failure"}
	job := &mockJob{name: "a.mkv"}

	first, _ := fm.HandleGPUError("x", enc, job)
	second, _ := fm.HandleGPUError("x", enc, job) // would be EOF -> true if re-prompted
	if first || second {
		t.Errorf("got first=%v second=%v, want both false (declined once, remembered)", first, second)
	}
	if fm.CPUOnly() {
		t.Error("CPUOnly should be false after the user declined")
	}
}

func TestFallbackNonInteractiveSetsCPUOnly(t *testing.T) {
	fm := NewFallbackManager(false, strings.NewReader(""), newTestLogger())
	if fm.CPUOnly() {
		t.Fatal("CPUOnly should start false")
	}
	enc := &mockEncoder{isGPU: true, msg: "gpu failure"}
	if ok, _ := fm.HandleGPUError("x", enc, &mockJob{name: "a"}); !ok {
		t.Fatal("expected fallback")
	}
	if !fm.CPUOnly() {
		t.Error("CPUOnly should be true after a handled GPU failure")
	}
}
