package pipeline

import (
	"sort"
	"sync"
)

// GPUAssigner distributes jobs across GPUs using a streaming weighted
// round-robin (deficit round-robin) algorithm. Unlike Distributor, which
// requires all jobs upfront, GPUAssigner assigns one job at a time as they
// arrive — preserving the streaming UX where the first conversion starts
// immediately.
//
// Over many calls, the distribution converges to the same proportions as
// Distributor.Assign for large N. For small N, exact proportionality barely
// matters.
//
// A nil *GPUAssigner is safe to call — Next() returns 0.
type GPUAssigner struct {
	mu      sync.Mutex
	gpus    []int     // sorted GPU indices
	weights []float64 // normalised weights (sum ≈ 1.0)
	credits []float64 // accumulated credits per GPU
}

// NewGPUAssigner creates a GPUAssigner from benchmark results.
// benchmarks maps gpuIndex → FPS. GPUs with zero or negative FPS are excluded.
// Returns nil when benchmarks is nil, empty, or all GPUs have zero speed —
// callers should nil-check or just call Next() which returns 0 for nil receivers.
func NewGPUAssigner(benchmarks map[int]float64) *GPUAssigner {
	if len(benchmarks) == 0 {
		return nil
	}

	var totalFPS float64
	var gpus []int
	for idx, fps := range benchmarks {
		if fps > 0 {
			gpus = append(gpus, idx)
			totalFPS += fps
		}
	}

	if len(gpus) == 0 || totalFPS == 0 {
		return nil
	}

	sort.Ints(gpus)

	weights := make([]float64, len(gpus))
	credits := make([]float64, len(gpus))
	for i, idx := range gpus {
		weights[i] = benchmarks[idx] / totalFPS
	}

	return &GPUAssigner{
		gpus:    gpus,
		weights: weights,
		credits: credits,
	}
}

// Next returns the GPU index that should handle the next job.
// Safe to call on a nil receiver — returns 0 (the default GPU).
//
// The algorithm is deficit round-robin: each call adds each GPU's weight
// to its credit, then picks the GPU with the highest credit and subtracts 1.0.
// This ensures proportional distribution without needing the total job count.
func (a *GPUAssigner) Next() int {
	if a == nil {
		return 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	for i := range a.gpus {
		a.credits[i] += a.weights[i]
	}

	best := 0
	for i := 1; i < len(a.gpus); i++ {
		if a.credits[i] > a.credits[best] {
			best = i
		}
	}

	a.credits[best] -= 1.0
	return a.gpus[best]
}

// NumGPUs returns the number of GPUs this assigner distributes across.
// Returns 0 for a nil receiver.
func (a *GPUAssigner) NumGPUs() int {
	if a == nil {
		return 0
	}
	return len(a.gpus)
}

// GPUs returns a copy of the sorted GPU indices this assigner distributes
// across. Returns nil for a nil receiver.
func (a *GPUAssigner) GPUs() []int {
	if a == nil {
		return nil
	}
	return append([]int(nil), a.gpus...)
}
