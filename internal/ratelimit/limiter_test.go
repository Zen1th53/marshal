package ratelimit

import (
	"testing"
	"time"
)

func TestRateLimiterTokenBucketAndPrincipalIsolation(t *testing.T) {
	rl := NewRateLimiter(10, 2, 1*time.Minute) // 10 rps, burst 2

	// Principal A consumes burst (2 requests)
	if ok, _ := rl.Allow("principal-a"); !ok {
		t.Fatal("expected 1st request for principal-a to be allowed")
	}
	if ok, _ := rl.Allow("principal-a"); !ok {
		t.Fatal("expected 2nd request for principal-a to be allowed")
	}
	// 3rd immediate request for principal-a should be rejected
	if ok, retryAfter := rl.Allow("principal-a"); ok {
		t.Fatal("expected 3rd request for principal-a to be rejected")
	} else if retryAfter <= 0 {
		t.Fatalf("expected positive retryAfter, got %v", retryAfter)
	}

	// Principal B should NOT be affected by principal A exhaustion (isolation)
	if ok, _ := rl.Allow("principal-b"); !ok {
		t.Fatal("expected principal-b to be allowed independently")
	}
}

func TestRateLimiterCleanupStaleBuckets(t *testing.T) {
	rl := NewRateLimiter(10, 5, 10*time.Millisecond)

	rl.Allow("stale-principal")
	time.Sleep(20 * time.Millisecond)
	rl.Cleanup()

	rl.mu.Lock()
	count := len(rl.buckets)
	rl.mu.Unlock()

	if count != 0 {
		t.Fatalf("expected 0 buckets after cleanup, got %d", count)
	}
}

func TestConcurrencyLimiter(t *testing.T) {
	cl := NewConcurrencyLimiter(2)

	if !cl.TryAcquire() {
		t.Fatal("expected 1st acquire to succeed")
	}
	if !cl.TryAcquire() {
		t.Fatal("expected 2nd acquire to succeed")
	}
	if cl.TryAcquire() {
		t.Fatal("expected 3rd acquire to fail due to capacity ceiling")
	}

	cl.Release()
	if !cl.TryAcquire() {
		t.Fatal("expected acquire to succeed after release")
	}
}

func TestIdempotencyStore(t *testing.T) {
	store := NewIdempotencyStore(50 * time.Millisecond)

	store.Set("msg-1", []byte(`{"status":"success"}`))

	res, ok := store.Get("msg-1")
	if !ok || string(res) != `{"status":"success"}` {
		t.Fatalf("expected stored response, got ok=%v, res=%s", ok, string(res))
	}

	time.Sleep(60 * time.Millisecond)
	_, ok = store.Get("msg-1")
	if ok {
		t.Fatal("expected expired entry to be evicted")
	}
}
