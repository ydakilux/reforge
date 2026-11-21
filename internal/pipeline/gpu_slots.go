package pipeline

import (
	"context"
	"sort"
	"sync"
)

// GPUSlots enforces a maximum number of concurrent encodes per GPU.
//
// GPUAssigner picks a preferred GPU when a job is enqueued, but jobs are then
// consumed by a shared worker pool, so without a limit enforced at execution
// time one GPU can end up running more simultaneous encodes than configured
// (and exceed the NVENC session limit). Acquire is called by the worker just
// before starting FFmpeg: it grants the preferred GPU when it has a free slot,
// otherwise the least-loaded GPU with a free slot, otherwise it waits.
//
// A nil *GPUSlots is valid and means "no limit": Acquire returns the preferred
// index immediately.
type GPUSlots struct {
	mu     sync.Mutex
	gpus   []int       // sorted GPU indices
	limit  int         // max concurrent encodes per GPU
	active map[int]int // in-flight encodes per GPU
	wake   chan struct{}
}

// NewGPUSlots creates a GPUSlots for the given GPU indices with at most
// perGPU concurrent encodes each. Returns nil when there are no GPUs or
// perGPU < 1 (no limiting).
func NewGPUSlots(gpus []int, perGPU int) *GPUSlots {
	if len(gpus) == 0 || perGPU < 1 {
		return nil
	}
	sorted := append([]int(nil), gpus...)
	sort.Ints(sorted)
	return &GPUSlots{
		gpus:   sorted,
		limit:  perGPU,
		active: make(map[int]int, len(sorted)),
		wake:   make(chan struct{}),
	}
}

// Acquire reserves an encode slot, preferring GPU `preferred`. It returns the
// GPU index actually granted and a release function that must be called
// exactly once when the encode finishes. It returns ctx.Err() if the context
// is cancelled while waiting.
func (s *GPUSlots) Acquire(ctx context.Context, preferred int) (int, func(), error) {
	if s == nil {
		return preferred, func() {}, nil
	}
	for {
		s.mu.Lock()
		if gpu, ok := s.pickLocked(preferred); ok {
			s.active[gpu]++
			s.mu.Unlock()
			var once sync.Once
			return gpu, func() { once.Do(func() { s.release(gpu) }) }, nil
		}
		wake := s.wake
		s.mu.Unlock()

		select {
		case <-wake:
		case <-ctx.Done():
			return preferred, func() {}, ctx.Err()
		}
	}
}

// pickLocked chooses a GPU with a free slot. Caller must hold s.mu.
func (s *GPUSlots) pickLocked(preferred int) (int, bool) {
	if s.hasGPU(preferred) && s.active[preferred] < s.limit {
		return preferred, true
	}
	best, bestLoad := 0, -1
	for _, g := range s.gpus {
		load := s.active[g]
		if load >= s.limit {
			continue
		}
		if bestLoad == -1 || load < bestLoad {
			best, bestLoad = g, load
		}
	}
	return best, bestLoad != -1
}

func (s *GPUSlots) hasGPU(idx int) bool {
	for _, g := range s.gpus {
		if g == idx {
			return true
		}
	}
	return false
}

func (s *GPUSlots) release(gpu int) {
	s.mu.Lock()
	if s.active[gpu] > 0 {
		s.active[gpu]--
	}
	close(s.wake)
	s.wake = make(chan struct{})
	s.mu.Unlock()
}
