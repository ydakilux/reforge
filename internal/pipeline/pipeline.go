// Package pipeline implements a multi-consumer job processing pipeline
// with injected handler functions. It is encoder-agnostic and knows nothing
// about FFmpeg, databases, or specific conversion logic.
package pipeline

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/ydakilux/reforge/internal/types"
)

// ErrStopped is returned by Submit when the controller has been told to stop
// (after current jobs or immediately) so producers can end their scan early.
var ErrStopped = errors.New("pipeline stopped")

// ConversionResult holds the outcome of a single conversion job.
type ConversionResult struct {
	Job        types.Job
	ExitCode   int
	Stderr     string
	OutputPath string
	OutputSize int64
	Duration   time.Duration
	Error      error
}

// Pipeline implements a multi-consumer producer-consumer pipeline.
// Jobs are submitted via Submit and processed by consumer goroutines
// that call an injected handler function. Results are collected via Results.
type Pipeline struct {
	jobs      chan types.Job
	results   chan ConversionResult
	wg        sync.WaitGroup
	consumers int
	ctx       context.Context
	cancel    context.CancelFunc
	ctrl      *Controller
}

// NewPipeline creates a new Pipeline with the given number of consumers
// and buffer sizes. The context is used for cancellation.
func NewPipeline(ctx context.Context, consumers int, bufferSize int) *Pipeline {
	derivedCtx, cancel := context.WithCancel(ctx)

	// Use consumers*2 for results buffer to give consumers breathing room
	resultsBuf := bufferSize
	if consumers*2 > resultsBuf {
		resultsBuf = consumers * 2
	}

	return &Pipeline{
		jobs:      make(chan types.Job, bufferSize),
		results:   make(chan ConversionResult, resultsBuf),
		consumers: consumers,
		ctx:       derivedCtx,
		cancel:    cancel,
		ctrl:      NewController(),
	}
}

// Controller returns the pipeline's [Controller], which can be used to
// pause, resume, or stop processing from external goroutines (e.g. the TUI).
func (p *Pipeline) Controller() *Controller { return p.ctrl }

// Start launches consumer goroutines that read jobs from the jobs channel
// and call the provided handler for each job. Each consumer runs until
// the jobs channel is closed or the context is cancelled.
func (p *Pipeline) Start(handler func(ctx context.Context, job types.Job) ConversionResult) {
	p.wg.Add(p.consumers)
	for i := 0; i < p.consumers; i++ {
		go func() {
			defer p.wg.Done()
			for job := range p.jobs {
				// Block here while paused; return if StopNow was called.
				if !p.ctrl.CheckPause() {
					return
				}
				// StopAfterCurrent: skip queued jobs but let in-flight finish.
				if p.ctx.Err() != nil {
					return
				}
				result := handler(p.ctx, job)
				p.results <- result
			}
		}()
	}
}

// Submit sends a job to the pipeline for processing. It blocks if the
// jobs channel buffer is full. Returns ctx.Err() if the context is
// cancelled before the job can be submitted. Returns ErrStopped if the
// controller has been told to stop (either mode).
func (p *Pipeline) Submit(job types.Job) error {
	if p.ctrl.Mode() != StopModeNone {
		return ErrStopped
	}
	select {
	case p.jobs <- job:
		return nil
	case <-p.ctx.Done():
		return p.ctx.Err()
	}
}

// Results returns a read-only channel of ConversionResults.
// Consumers should range over this channel to collect results.
func (p *Pipeline) Results() <-chan ConversionResult {
	return p.results
}

// Wait closes the jobs channel (signaling no more jobs will be submitted),
// waits for all consumer goroutines to finish, then closes the results channel.
// The order is critical: close jobs → wait for consumers → close results.
func (p *Pipeline) Wait() {
	close(p.jobs)
	p.wg.Wait()
	close(p.results)
}

// Stop cancels the pipeline context, signaling all consumers to stop
// processing. It does NOT close channels directly — Wait handles that.
// For a graceful "finish current jobs" stop, call ctrl.StopAfterCurrent()
// followed by Wait(); for immediate cancellation, call ctrl.StopNow() then Stop().
func (p *Pipeline) Stop() {
	p.cancel()
}
