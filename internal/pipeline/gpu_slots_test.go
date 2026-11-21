package pipeline

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGPUSlots_NilIsUnlimited(t *testing.T) {
	var s *GPUSlots
	gpu, release, err := s.Acquire(context.Background(), 3)
	if err != nil || gpu != 3 {
		t.Fatalf("got (%d, %v), want (3, nil)", gpu, err)
	}
	release()
}

func TestGPUSlots_PrefersAssignedThenSpillsOver(t *testing.T) {
	s := NewGPUSlots([]int{0, 1}, 1)
	g0, r0, _ := s.Acquire(context.Background(), 0)
	g1, r1, _ := s.Acquire(context.Background(), 0) // GPU0 full -> spills to GPU1
	if g0 != 0 || g1 != 1 {
		t.Fatalf("got GPUs %d,%d want 0,1", g0, g1)
	}
	r0()
	r1()
}

func TestGPUSlots_BlocksWhenFullAndWakesOnRelease(t *testing.T) {
	s := NewGPUSlots([]int{0}, 1)
	_, release, _ := s.Acquire(context.Background(), 0)

	got := make(chan struct{})
	go func() {
		_, r, _ := s.Acquire(context.Background(), 0)
		r()
		close(got)
	}()
	select {
	case <-got:
		t.Fatal("second Acquire should block while the slot is held")
	case <-time.After(50 * time.Millisecond):
	}
	release()
	select {
	case <-got:
	case <-time.After(time.Second):
		t.Fatal("second Acquire did not wake after release")
	}
}

func TestGPUSlots_ContextCancelWhileWaiting(t *testing.T) {
	s := NewGPUSlots([]int{0}, 1)
	_, release, _ := s.Acquire(context.Background(), 0)
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, _, err := s.Acquire(ctx, 0); err == nil {
		t.Fatal("expected context error")
	}
}

func TestGPUSlots_NeverExceedsLimit(t *testing.T) {
	const perGPU = 2
	s := NewGPUSlots([]int{0, 1}, perGPU)
	var cur [2]int32
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			gpu, release, _ := s.Acquire(context.Background(), i%2)
			if n := atomic.AddInt32(&cur[gpu], 1); n > perGPU {
				t.Errorf("GPU %d has %d concurrent encodes (limit %d)", gpu, n, perGPU)
			}
			time.Sleep(2 * time.Millisecond)
			atomic.AddInt32(&cur[gpu], -1)
			release()
		}(i)
	}
	wg.Wait()
}

func TestGPUSlots_ReleaseIsIdempotent(t *testing.T) {
	s := NewGPUSlots([]int{0}, 1)
	_, release, _ := s.Acquire(context.Background(), 0)
	release()
	release() // must not free a slot twice
	_, r1, _ := s.Acquire(context.Background(), 0)
	defer r1()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, _, err := s.Acquire(ctx, 0); err == nil {
		t.Fatal("double release freed an extra slot")
	}
}
