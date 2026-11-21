package pipeline

import "sync"

// StopMode describes how the pipeline should stop.
type StopMode int

const (
	StopModeNone         StopMode = iota // not stopping
	StopModeAfterCurrent                 // finish in-flight jobs, skip queued ones
	StopModeNow                          // cancel everything immediately
)

// Controller is a thread-safe pause/resume/stop signal shared between the
// pipeline workers, the FFmpeg runner, and the TUI.
//
// Workers call [Controller.CheckPause] at the start of each job — this blocks
// while the controller is paused and returns false when the pipeline should
// stop entirely.
//
// FFmpeg callers register a [SuspendFunc] via [Controller.SetSuspendFunc] so
// the running FFmpeg process is also suspended while paused.
type Controller struct {
	mu      sync.Mutex
	cond    *sync.Cond
	paused  bool
	stopped bool
	mode    StopMode

	// suspendFns holds one suspend/resume function per active job.
	// The function receives true to suspend and false to resume.
	suspendFns map[uint64]func(suspend bool)
	nextFnID   uint64
}

// NewController returns a ready-to-use Controller.
func NewController() *Controller {
	c := &Controller{
		suspendFns: make(map[uint64]func(suspend bool)),
	}
	c.cond = sync.NewCond(&c.mu)
	return c
}

// ── Worker API ────────────────────────────────────────────────────────────────

// CheckPause is called by a pipeline worker before it starts a new job.
// It blocks while the controller is paused. It returns false when the worker
// should stop without processing any more jobs (StopNow or StopAfterCurrent).
func (c *Controller) CheckPause() (shouldContinue bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for c.paused && !c.stopped {
		c.cond.Wait()
	}
	return !c.stopped
}

// RegisterSuspendFn registers a suspend/resume callback for an active job and
// returns an ID to use when unregistering. The callback is called with true
// immediately if the controller is currently paused.
func (c *Controller) RegisterSuspendFn(fn func(suspend bool)) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.nextFnID
	c.nextFnID++
	c.suspendFns[id] = fn
	if c.paused {
		fn(true) // immediately suspend the new process
	}
	return id
}

// UnregisterSuspendFn removes the suspend callback for the given ID.
func (c *Controller) UnregisterSuspendFn(id uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.suspendFns, id)
}

// ── UI API ────────────────────────────────────────────────────────────────────

// Pause suspends all workers and running FFmpeg processes.
// No-op if already paused or stopped.
func (c *Controller) Pause() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped || c.paused {
		return
	}
	c.paused = true
	for _, fn := range c.suspendFns {
		fn(true)
	}
}

// Resume un-suspends workers and FFmpeg processes.
// No-op if not paused.
func (c *Controller) Resume() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.paused {
		return
	}
	c.paused = false
	for _, fn := range c.suspendFns {
		fn(false)
	}
	c.cond.Broadcast()
}

// StopNow cancels everything. Workers that are currently blocked in
// CheckPause will unblock and return false. In-flight FFmpeg processes
// will be killed by the pipeline's context cancellation (handled externally).
func (c *Controller) StopNow() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopped = true
	c.resumeSuspendedLocked()
	c.mode = StopModeNow
	c.cond.Broadcast()
}

// StopAfterCurrent signals that no new jobs should be started but in-flight
// jobs should finish. Workers check IsStopped() after each job.
func (c *Controller) StopAfterCurrent() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopped = true
	c.resumeSuspendedLocked()
	c.mode = StopModeAfterCurrent
	c.cond.Broadcast()
}

// resumeSuspendedLocked clears the paused flag and un-suspends every running
// FFmpeg process. Without this, stopping while paused would leave in-flight
// processes suspended forever (Resume is a no-op once paused is false), so
// "finish current jobs" would never complete. Caller must hold c.mu.
func (c *Controller) resumeSuspendedLocked() {
	if !c.paused {
		return
	}
	c.paused = false
	for _, fn := range c.suspendFns {
		fn(false)
	}
}

// ── State queries ─────────────────────────────────────────────────────────────

// IsPaused reports whether the controller is currently paused.
func (c *Controller) IsPaused() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.paused
}

// Mode returns the current stop mode.
func (c *Controller) Mode() StopMode {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.mode
}
