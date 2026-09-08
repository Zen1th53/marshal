package execution

import (
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// RateLimitRecord records authoritative rate limit signals received from a provider.
type RateLimitRecord struct {
	Harness          string        `json:"harness"`
	Model            string        `json:"model"`
	HTTPStatus       int           `json:"http_status"`
	RetryAfter       time.Duration `json:"retry_after"`
	ResetTime        time.Time     `json:"reset_time,omitempty"`
	ObservedAt       time.Time     `json:"observed_at"`
	Source           string        `json:"source"`
	IsQuotaExhausted bool          `json:"is_quota_exhausted"`
}

// ParseRetryAfter parses standard HTTP Retry-After headers (seconds or HTTP date).
func ParseRetryAfter(headerVal string, now time.Time) (time.Duration, error) {
	if headerVal == "" {
		return 0, fmt.Errorf("empty retry-after header")
	}

	// First try integer seconds
	if secs, err := strconv.ParseFloat(headerVal, 64); err == nil {
		if secs < 0 {
			return 0, fmt.Errorf("negative retry-after value: %f", secs)
		}
		return time.Duration(secs * float64(time.Second)), nil
	}

	// Next try HTTP dates (RFC1123, RFC850, ANSIC)
	formats := []string{
		http.TimeFormat,
		time.RFC850,
		time.ANSIC,
	}

	for _, format := range formats {
		if t, err := time.Parse(format, headerVal); err == nil {
			if t.Before(now) {
				return 0, nil
			}
			return t.Sub(now), nil
		}
	}

	return 0, fmt.Errorf("unrecognized retry-after format: %q", headerVal)
}

// BackoffCalculator computes bounded exponential backoff with jitter, respecting Retry-After floor.
type BackoffCalculator struct {
	BaseDelay  time.Duration
	MaxDelay   time.Duration
	JitterFrac float64
}

// DefaultBackoffCalculator provides conservative defaults to prevent retry storms.
func DefaultBackoffCalculator() *BackoffCalculator {
	return &BackoffCalculator{
		BaseDelay:  500 * time.Millisecond,
		MaxDelay:   30 * time.Second,
		JitterFrac: 0.2,
	}
}

// CalculateDelay returns delay duration for a given attempt index and optional retry-after floor.
func (b *BackoffCalculator) CalculateDelay(attempt int, retryAfterFloor time.Duration) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	exp := math.Pow(2, float64(attempt))
	delaySecs := b.BaseDelay.Seconds() * exp
	maxSecs := b.MaxDelay.Seconds()
	if delaySecs > maxSecs {
		delaySecs = maxSecs
	}

	// Apply jitter: +/- JitterFrac
	if b.JitterFrac > 0 {
		jitter := (rand.Float64()*2 - 1) * b.JitterFrac * delaySecs
		delaySecs += jitter
		if delaySecs < 0 {
			delaySecs = b.BaseDelay.Seconds()
		}
	}

	calcDuration := time.Duration(delaySecs * float64(time.Second))
	if retryAfterFloor > calcDuration {
		return retryAfterFloor
	}
	return calcDuration
}

// RateLimitTracker tracks live rate limits across provider harnesses and models.
type RateLimitTracker struct {
	mu      sync.RWMutex
	records map[string]RateLimitRecord // key: harness or harness:model
}

// NewRateLimitTracker creates an empty tracker.
func NewRateLimitTracker() *RateLimitTracker {
	return &RateLimitTracker{
		records: make(map[string]RateLimitRecord),
	}
}

// RecordRateLimit stores an authoritative rate limit event.
func (r *RateLimitTracker) RecordRateLimit(rec RateLimitRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := rec.Harness
	if rec.Model != "" {
		key = fmt.Sprintf("%s:%s", rec.Harness, rec.Model)
	}
	r.records[key] = rec
}

// IsThrottled checks if the given harness/model is currently under rate limit cooldown.
func (r *RateLimitTracker) IsThrottled(harness, model string, now time.Time) (bool, time.Duration) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	keys := []string{
		fmt.Sprintf("%s:%s", harness, model),
		harness,
	}

	for _, k := range keys {
		if rec, exists := r.records[k]; exists {
			cooldownUntil := rec.ObservedAt.Add(rec.RetryAfter)
			if !rec.ResetTime.IsZero() && rec.ResetTime.After(cooldownUntil) {
				cooldownUntil = rec.ResetTime
			}
			if now.Before(cooldownUntil) {
				return true, cooldownUntil.Sub(now)
			}
		}
	}

	return false, 0
}

// SelectAvailableWorker inspects the task's assigned harness and fallback harnesses,
// returning the first available harness not currently throttled.
func (r *RateLimitTracker) SelectAvailableWorker(task *TaskExecution, now time.Time) (string, bool) {
	primary := task.AssignedHarness
	if throttled, _ := r.IsThrottled(primary, task.AssignedModel, now); !throttled {
		return primary, true
	}

	// Check fallback harnesses defined in plan / task metadata
	for _, fallback := range task.FallbackHarnesses {
		if throttled, _ := r.IsThrottled(fallback, "", now); !throttled {
			return fallback, true
		}
	}

	return "", false
}
