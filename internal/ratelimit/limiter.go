package ratelimit

import (
	"math"
	"sync"
	"time"
)

type bucket struct {
	tokens     float64
	lastUpdate time.Time
}

type RateLimiter struct {
	mu      sync.Mutex
	rate    float64 // tokens per second
	burst   int     // max bucket capacity
	ttl     time.Duration
	buckets map[string]*bucket
	stopCh  chan struct{}
}

func NewRateLimiter(rate float64, burst int, ttl time.Duration) *RateLimiter {
	rl := &RateLimiter{
		rate:    rate,
		burst:   burst,
		ttl:     ttl,
		buckets: make(map[string]*bucket),
		stopCh:  make(chan struct{}),
	}
	return rl
}

func (rl *RateLimiter) Allow(key string) (bool, time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	b, exists := rl.buckets[key]
	if !exists {
		b = &bucket{
			tokens:     float64(rl.burst),
			lastUpdate: now,
		}
		rl.buckets[key] = b
	} else {
		elapsed := now.Sub(b.lastUpdate).Seconds()
		b.tokens = math.Min(float64(rl.burst), b.tokens+(elapsed*rl.rate))
		b.lastUpdate = now
	}

	if b.tokens >= 1.0 {
		b.tokens -= 1.0
		return true, 0
	}

	missing := 1.0 - b.tokens
	retryAfter := time.Duration(missing/rl.rate*float64(time.Second)) + (10 * time.Millisecond)
	return false, retryAfter
}

func (rl *RateLimiter) Cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	for k, b := range rl.buckets {
		if now.Sub(b.lastUpdate) > rl.ttl {
			delete(rl.buckets, k)
		}
	}
}

type ConcurrencyLimiter struct {
	sem chan struct{}
}

func NewConcurrencyLimiter(maxConcurrent int) *ConcurrencyLimiter {
	if maxConcurrent <= 0 {
		maxConcurrent = 100
	}
	return &ConcurrencyLimiter{
		sem: make(chan struct{}, maxConcurrent),
	}
}

func (cl *ConcurrencyLimiter) TryAcquire() bool {
	select {
	case cl.sem <- struct{}{}:
		return true
	default:
		return false
	}
}

func (cl *ConcurrencyLimiter) Release() {
	select {
	case <-cl.sem:
	default:
	}
}
