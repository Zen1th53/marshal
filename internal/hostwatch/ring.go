package hostwatch

import "sync"

// Ring is a thread-safe ring buffer keeping the last N samples.
type Ring struct {
	mu       sync.RWMutex
	capacity int
	samples  []Sample
	start    int
}

// NewRing constructs a Ring buffer with the given capacity.
func NewRing(capacity int) *Ring {
	if capacity <= 0 {
		capacity = 1
	}
	return &Ring{
		capacity: capacity,
		samples:  make([]Sample, 0, capacity),
	}
}

// Push adds a sample to the ring buffer.
func (r *Ring) Push(s Sample) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.samples) < r.capacity {
		r.samples = append(r.samples, s)
	} else {
		r.samples[r.start] = s
		r.start = (r.start + 1) % r.capacity
	}
}

// Samples returns a copy of all buffered samples in chronological order.
func (r *Ring) Samples() []Sample {
	r.mu.RLock()
	defer r.mu.RUnlock()

	n := len(r.samples)
	if n == 0 {
		return nil
	}
	out := make([]Sample, n)
	if n < r.capacity {
		copy(out, r.samples)
	} else {
		// First copy from start to capacity, then from 0 to start
		head := copy(out, r.samples[r.start:])
		copy(out[head:], r.samples[:r.start])
	}
	return out
}

// Len returns the current number of samples stored.
func (r *Ring) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.samples)
}

// Cap returns the capacity of the ring buffer.
func (r *Ring) Cap() int {
	return r.capacity
}
