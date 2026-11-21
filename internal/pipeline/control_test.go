package pipeline

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ── Initial state ─────────────────────────────────────────────────────────────

func TestNewController(t *testing.T) {
	c := NewController()
	if c.IsPaused() {
		t.Error("new controller should not be paused")
	}
	if c.Mode() != StopModeNone {
		t.Errorf("new controller mode = %v, want StopModeNone", c.Mode())
	}
}

// ── Pause / Resume ────────────────────────────────────────────────────────────

func TestPauseResume(t *testing.T) {
	c := NewController()
	c.Pause()
	if !c.IsPaused() {
		t.Error("after Pause: IsPaused should be true")
	}
	if c.Mode() != StopModeNone {
		t.Errorf("Pause should not change Mode; got %v", c.Mode())
	}
	c.Resume()
	if c.IsPaused() {
		t.Error("after Resume: IsPaused should be false")
	}
}

func TestPauseIdempotent(t *testing.T) {
	c := NewController()
	var calls int
	c.RegisterSuspendFn(func(suspend bool) {
		if suspend {
			calls++
		}
	})
	c.Pause()
	c.Pause() // second Pause should be no-op
	if !c.IsPaused() {
		t.Error("should still be paused")
	}
	if calls != 1 {
		t.Errorf("suspend fn called %d times for double Pause, want 1", calls)
	}
}

func TestResumeIdempotent(t *testing.T) {
	c := NewController()
	// Resume on un-paused controller should not panic
	c.Resume()
	if c.IsPaused() {
		t.Error("IsPaused should be false after Resume on un-paused")
	}
}

// ── StopNow / StopAfterCurrent ────────────────────────────────────────────────

func TestStopNow(t *testing.T) {
	c := NewController()
	c.StopNow()
	if c.Mode() != StopModeNow {
		t.Errorf("after StopNow: Mode=%v, want StopModeNow", c.Mode())
	}
	if c.IsPaused() {
		t.Error("StopNow should clear paused")
	}
}

func TestStopNowIdempotent(t *testing.T) {
	c := NewController()
	c.StopNow()
	c.StopNow() // second call should not panic
	if c.Mode() != StopModeNow {
		t.Errorf("Mode should still be StopModeNow, got %v", c.Mode())
	}
}

func TestStopAfterCurrent(t *testing.T) {
	c := NewController()
	c.StopAfterCurrent()
	if c.Mode() != StopModeAfterCurrent {
		t.Errorf("after StopAfterCurrent: Mode=%v, want StopModeAfterCurrent", c.Mode())
	}
	if c.IsPaused() {
		t.Error("StopAfterCurrent should clear paused")
	}
}

func TestPauseNoOpAfterStop(t *testing.T) {
	c := NewController()
	c.StopNow()
	c.Pause() // should be no-op since stopped
	if c.IsPaused() {
		t.Error("Pause after Stop should be no-op; IsPaused should remain false")
	}
}

// ── SuspendFn ─────────────────────────────────────────────────────────────────

func TestRegisterSuspendFnCalledOnPause(t *testing.T) {
	c := NewController()
	var got []bool
	c.RegisterSuspendFn(func(suspend bool) { got = append(got, suspend) })
	c.Pause()
	if len(got) != 1 || !got[0] {
		t.Errorf("suspend fn should be called with true on Pause; got %v", got)
	}
}

func TestRegisterSuspendFnCalledOnResume(t *testing.T) {
	c := NewController()
	var got []bool
	c.RegisterSuspendFn(func(suspend bool) { got = append(got, suspend) })
	c.Pause()
	c.Resume()
	if len(got) != 2 || got[1] != false {
		t.Errorf("suspend fn should be called with false on Resume; got %v", got)
	}
}

func TestRegisterSuspendFnImmediateIfPaused(t *testing.T) {
	c := NewController()
	c.Pause()
	var got []bool
	c.RegisterSuspendFn(func(suspend bool) { got = append(got, suspend) })
	// Should have been called immediately with true
	if len(got) != 1 || !got[0] {
		t.Errorf("fn should be called immediately with true when already paused; got %v", got)
	}
}

func TestUnregisterSuspendFn(t *testing.T) {
	c := NewController()
	var calls int
	id := c.RegisterSuspendFn(func(suspend bool) { calls++ })
	c.Pause()
	c.UnregisterSuspendFn(id)
	c.Resume() // should NOT call the fn since it was unregistered
	if calls != 1 {
		t.Errorf("fn should be called exactly once (for Pause), got %d", calls)
	}
}

func TestRegisterMultipleSuspendFns(t *testing.T) {
	c := NewController()
	var mu sync.Mutex
	pauseCalls := 0
	resumeCalls := 0
	for i := 0; i < 3; i++ {
		c.RegisterSuspendFn(func(suspend bool) {
			mu.Lock()
			defer mu.Unlock()
			if suspend {
				pauseCalls++
			} else {
				resumeCalls++
			}
		})
	}
	c.Pause()
	c.Resume()
	mu.Lock()
	defer mu.Unlock()
	if pauseCalls != 3 {
		t.Errorf("pauseCalls=%d, want 3", pauseCalls)
	}
	if resumeCalls != 3 {
		t.Errorf("resumeCalls=%d, want 3", resumeCalls)
	}
}

// ── CheckPause (blocking) ─────────────────────────────────────────────────────

func TestCheckPauseReturnsTrueWhenNotPaused(t *testing.T) {
	c := NewController()
	done := make(chan bool, 1)
	go func() { done <- c.CheckPause() }()
	select {
	case result := <-done:
		if !result {
			t.Error("CheckPause should return true when not paused or stopped")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("CheckPause blocked unexpectedly on non-paused controller")
	}
}

func TestCheckPauseReturnsFalseAfterStopAfterCurrent(t *testing.T) {
	c := NewController()
	c.StopAfterCurrent()
	done := make(chan bool, 1)
	go func() { done <- c.CheckPause() }()
	select {
	case result := <-done:
		if result {
			t.Error("CheckPause should return false after StopAfterCurrent")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("CheckPause blocked after StopAfterCurrent")
	}
}

func TestCheckPauseReturnsFalseAfterStopNow(t *testing.T) {
	c := NewController()
	c.StopNow()
	done := make(chan bool, 1)
	go func() { done <- c.CheckPause() }()
	select {
	case result := <-done:
		if result {
			t.Error("CheckPause should return false after StopNow")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("CheckPause blocked after StopNow")
	}
}

func TestCheckPauseBlocksWhilePaused(t *testing.T) {
	c := NewController()
	c.Pause()

	started := make(chan struct{})
	done := make(chan bool, 1)
	go func() {
		close(started)
		done <- c.CheckPause()
	}()

	<-started
	// Give goroutine time to block inside CheckPause
	time.Sleep(60 * time.Millisecond)

	select {
	case <-done:
		t.Fatal("CheckPause should still be blocking while paused")
	default:
		// Good — still blocked
	}

	c.Resume()
	select {
	case result := <-done:
		if !result {
			t.Error("CheckPause should return true after Resume")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CheckPause did not unblock after Resume")
	}
}

func TestCheckPauseUnblocksOnStopNow(t *testing.T) {
	c := NewController()
	c.Pause()

	started := make(chan struct{})
	done := make(chan bool, 1)
	go func() {
		close(started)
		done <- c.CheckPause()
	}()

	<-started
	time.Sleep(60 * time.Millisecond)

	c.StopNow()
	select {
	case result := <-done:
		if result {
			t.Error("CheckPause should return false after StopNow unblocks it")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CheckPause did not unblock after StopNow")
	}
}

// ── Concurrency / race detector ───────────────────────────────────────────────

func TestConcurrentPauseResume(t *testing.T) {
	c := NewController()
	var wg sync.WaitGroup
	const goroutines = 10
	const iters = 50

	stopFlag := atomic.Bool{}

	// Separate goroutine calling CheckPause in a loop
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stopFlag.Load() {
			// non-blocking: only call CheckPause if not stopped
			c.mu.Lock()
			paused := c.paused
			stopped := c.stopped
			c.mu.Unlock()
			if paused && !stopped {
				c.CheckPause()
			}
			time.Sleep(time.Microsecond)
		}
	}()

	// Goroutines doing Pause/Resume
	var opWg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		opWg.Add(1)
		go func() {
			defer opWg.Done()
			for j := 0; j < iters; j++ {
				c.Pause()
				c.Resume()
			}
		}()
	}
	opWg.Wait()

	stopFlag.Store(true)
	// Ensure CheckPause goroutine is not blocked
	c.StopNow()
	wg.Wait()
}

func TestConcurrentRegisterUnregister(t *testing.T) {
	c := NewController()
	var wg sync.WaitGroup
	const n = 20

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := c.RegisterSuspendFn(func(bool) {})
			c.Pause()
			c.UnregisterSuspendFn(id)
			c.Resume()
		}()
	}
	wg.Wait()
}

// ── Benchmark ─────────────────────────────────────────────────────────────────

func BenchmarkCheckPauseUncontested(b *testing.B) {
	c := NewController()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			c.CheckPause()
		}
	})
}

// Stopping while paused must resume suspended FFmpeg processes, otherwise
// "finish current jobs" can never complete.
func TestStopWhilePausedResumesSuspendedProcesses(t *testing.T) {
	for name, stop := range map[string]func(*Controller){
		"StopAfterCurrent": (*Controller).StopAfterCurrent,
		"StopNow":          (*Controller).StopNow,
	} {
		t.Run(name, func(t *testing.T) {
			c := NewController()
			suspended := false
			c.RegisterSuspendFn(func(s bool) { suspended = s })
			c.Pause()
			if !suspended {
				t.Fatal("precondition: process should be suspended after Pause")
			}
			stop(c)
			if suspended {
				t.Error("process still suspended after stop")
			}
			if c.IsPaused() {
				t.Error("controller still paused after stop")
			}
		})
	}
}
