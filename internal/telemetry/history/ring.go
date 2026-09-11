package history

import (
	"sync"

	"github.com/noteMASTER11/undervolt-go-studio/internal/telemetry"
)

// Ring stores a fixed number of the newest samples.
type Ring struct {
	mu      sync.RWMutex
	samples []telemetry.Sample
	next    int
	size    int
}

func New(capacity int) *Ring {
	if capacity < 1 {
		panic("history: capacity must be positive")
	}
	return &Ring{samples: make([]telemetry.Sample, capacity)}
}

func (r *Ring) Append(sample telemetry.Sample) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.samples[r.next] = sample
	r.next = (r.next + 1) % len(r.samples)
	if r.size < len(r.samples) {
		r.size++
	}
}

// Snapshot returns an oldest-to-newest copy of the stored samples.
func (r *Ring) Snapshot() []telemetry.Sample {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]telemetry.Sample, r.size)
	start := (r.next - r.size + len(r.samples)) % len(r.samples)
	for index := 0; index < r.size; index++ {
		result[index] = r.samples[(start+index)%len(r.samples)]
	}
	return result
}
