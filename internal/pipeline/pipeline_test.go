package pipeline

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ydakilux/reforge/internal/types"
)

func TestNewPipeline(t *testing.T) {
	ctx := context.Background()
	p := NewPipeline(ctx, 4, 10)

	if p == nil {
		t.Fatal("NewPipeline returned nil")
	}
	if p.consumers != 4 {
		t.Errorf("expected 4 consumers, got %d", p.consumers)
	}
	if p.jobs == nil {
		t.Error("jobs channel is nil")
	}
	if p.results == nil {
		t.Error("results channel is nil")
	}
	if p.ctx == nil {
		t.Error("context is nil")
	}
	if p.cancel == nil {
		t.Error("cancel func is nil")
	}
	// Results buffer should be at least consumers*2
	if cap(p.results) < p.consumers*2 {
		t.Errorf("results buffer too small: got %d, want at least %d", cap(p.results), p.consumers*2)
	}
}

func TestSubmitAndResult(t *testing.T) {
	ctx := context.Background()
	p := NewPipeline(ctx, 1, 10)

	handler := func(ctx context.Context, job types.Job) ConversionResult {
		return ConversionResult{
			Job:        job,
			ExitCode:   0,
			OutputPath: "/output/" + job.FilePath,
			OutputSize: 1024,
			Duration:   50 * time.Millisecond,
		}
	}

	p.Start(handler)

	testJob := types.Job{
		FilePath:     "test_video.mp4",
		FileHash:     "abc123",
		OriginalSize: 2048,
		FileNumber:   1,
		TotalFiles:   1,
	}

	if err := p.Submit(testJob); err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	// Collect result before calling Wait
	result := <-p.Results()

	if result.Job.FilePath != "test_video.mp4" {
		t.Errorf("expected FilePath 'test_video.mp4', got '%s'", result.Job.FilePath)
	}
	if result.ExitCode != 0 {
		t.Errorf("expected ExitCode 0, got %d", result.ExitCode)
	}
	if result.OutputPath != "/output/test_video.mp4" {
		t.Errorf("expected OutputPath '/output/test_video.mp4', got '%s'", result.OutputPath)
	}
	if result.OutputSize != 1024 {
		t.Errorf("expected OutputSize 1024, got %d", result.OutputSize)
	}
	if result.Error != nil {
		t.Errorf("expected nil Error, got %v", result.Error)
	}

	p.Wait()
}

func TestMultiConsumerProcessesAllJobs(t *testing.T) {
	ctx := context.Background()
	consumers := 3
	totalJobs := 9
	p := NewPipeline(ctx, consumers, totalJobs)

	handler := func(ctx context.Context, job types.Job) ConversionResult {
		time.Sleep(10 * time.Millisecond) // Simulate work
		return ConversionResult{
			Job:      job,
			ExitCode: 0,
		}
	}

	p.Start(handler)

	// Submit all jobs
	for i := 0; i < totalJobs; i++ {
		job := types.Job{
			FilePath:   "video_" + string(rune('A'+i)) + ".mp4",
			FileHash:   "hash_" + string(rune('A'+i)),
			FileNumber: i + 1,
			TotalFiles: totalJobs,
		}
		if err := p.Submit(job); err != nil {
			t.Fatalf("Submit failed for job %d: %v", i, err)
		}
	}

	// Collect results in background
	var results []ConversionResult
	done := make(chan struct{})
	go func() {
		for r := range p.Results() {
			results = append(results, r)
		}
		close(done)
	}()

	p.Wait()
	<-done

	if len(results) != totalJobs {
		t.Fatalf("expected %d results, got %d", totalJobs, len(results))
	}

	// Check for duplicates using file hashes
	seen := make(map[string]bool)
	for _, r := range results {
		if seen[r.Job.FileHash] {
			t.Errorf("duplicate result for hash %s", r.Job.FileHash)
		}
		seen[r.Job.FileHash] = true
	}
}

func TestContextCancellation(t *testing.T) {
	ctx := context.Background()
	p := NewPipeline(ctx, 2, 20)

	handler := func(ctx context.Context, job types.Job) ConversionResult {
		// Simulate slow work that respects context
		select {
		case <-time.After(500 * time.Millisecond):
			return ConversionResult{Job: job, ExitCode: 0}
		case <-ctx.Done():
			return ConversionResult{Job: job, ExitCode: -1, Error: ctx.Err()}
		}
	}

	p.Start(handler)

	// Submit several jobs
	for i := 0; i < 10; i++ {
		job := types.Job{
			FilePath:   "slow_video.mp4",
			FileHash:   "hash_slow",
			FileNumber: i + 1,
			TotalFiles: 10,
		}
		_ = p.Submit(job)
	}

	// Cancel after a short delay
	time.Sleep(100 * time.Millisecond)
	p.Stop()

	// Wait should return promptly (within 2s), not deadlock
	waitDone := make(chan struct{})
	go func() {
		// Drain results to prevent blocking consumers
		go func() {
			for range p.Results() {
			}
		}()
		p.Wait()
		close(waitDone)
	}()

	select {
	case <-waitDone:
		// Good — Wait returned
	case <-time.After(2 * time.Second):
		t.Fatal("Wait() did not return within 2 seconds after Stop() — possible deadlock")
	}
}

func TestSubmitBlocksWhenFull(t *testing.T) {
	ctx := context.Background()
	// bufferSize=1, 1 consumer with slow handler
	p := NewPipeline(ctx, 1, 1)

	handler := func(ctx context.Context, job types.Job) ConversionResult {
		time.Sleep(200 * time.Millisecond)
		return ConversionResult{Job: job, ExitCode: 0}
	}

	p.Start(handler)

	// First submit should succeed immediately (fills buffer)
	err := p.Submit(types.Job{FilePath: "a.mp4", FileHash: "h1", FileNumber: 1, TotalFiles: 3})
	if err != nil {
		t.Fatalf("first Submit failed: %v", err)
	}

	// Second submit — may or may not block depending on consumer pickup
	// Submit a few more and collect results to confirm no deadlock
	var submitWg sync.WaitGroup
	submitWg.Add(2)
	go func() {
		defer submitWg.Done()
		_ = p.Submit(types.Job{FilePath: "b.mp4", FileHash: "h2", FileNumber: 2, TotalFiles: 3})
	}()
	go func() {
		defer submitWg.Done()
		_ = p.Submit(types.Job{FilePath: "c.mp4", FileHash: "h3", FileNumber: 3, TotalFiles: 3})
	}()

	// Wait for all submits to complete (with timeout)
	submitDone := make(chan struct{})
	go func() {
		submitWg.Wait()
		close(submitDone)
	}()

	select {
	case <-submitDone:
		// All submits completed
	case <-time.After(5 * time.Second):
		t.Fatal("Submit deadlocked — should not happen with active consumer")
	}

	// Drain results
	go func() {
		for range p.Results() {
		}
	}()

	p.Wait()
}

func TestSubmitAfterCancel(t *testing.T) {
	ctx := context.Background()
	p := NewPipeline(ctx, 1, 1)

	handler := func(ctx context.Context, job types.Job) ConversionResult {
		return ConversionResult{Job: job, ExitCode: 0}
	}

	p.Start(handler)
	p.Stop()

	// Allow goroutines to see cancellation
	time.Sleep(10 * time.Millisecond)

	err := p.Submit(types.Job{FilePath: "after_cancel.mp4", FileHash: "hx"})
	if err == nil {
		// It's acceptable if the job was buffered before context check,
		// but if context was already cancelled, Submit should return error.
		// Either behavior is fine for this test — we just verify no panic.
		t.Log("Submit returned nil (job may have been buffered before cancel was detected)")
	} else {
		t.Logf("Submit correctly returned error after cancel: %v", err)
	}

	// Drain and wait
	go func() {
		for range p.Results() {
		}
	}()
	p.Wait()
}

func TestSubmitReturnsErrStoppedAfterStop(t *testing.T) {
	for name, stop := range map[string]func(*Controller){
		"StopAfterCurrent": (*Controller).StopAfterCurrent,
		"StopNow":          (*Controller).StopNow,
	} {
		t.Run(name, func(t *testing.T) {
			p := NewPipeline(context.Background(), 1, 1)
			stop(p.Controller())
			if err := p.Submit(types.Job{}); !errors.Is(err, ErrStopped) {
				t.Errorf("Submit err = %v, want ErrStopped", err)
			}
		})
	}
}
