package protocol

import (
	"sync"
	"time"
)

// Metrics is a bounded operational projection. It never decides whether a
// handoff is authorized or durable.
type Metrics struct {
	mu       sync.RWMutex
	snapshot MetricsSnapshot
}

type MetricsSnapshot struct {
	Accepted      uint64
	Denied        map[ErrorCode]uint64
	Invalid       map[ErrorCode]uint64
	Errors        map[ErrorCode]uint64
	Active        uint64
	DurationNanos uint64
	LastFailure   ErrorCode
}

func NewMetrics() *Metrics {
	return &Metrics{snapshot: MetricsSnapshot{
		Denied: make(map[ErrorCode]uint64), Invalid: make(map[ErrorCode]uint64), Errors: make(map[ErrorCode]uint64),
	}}
}

func (m *Metrics) observe(err error, duration time.Duration, accepted, consumed bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if duration > 0 {
		m.snapshot.DurationNanos += uint64(duration)
	}
	if err == nil {
		if accepted {
			m.snapshot.Accepted++
			m.snapshot.Active++
		}
		if consumed && m.snapshot.Active > 0 {
			m.snapshot.Active--
		}
		return
	}
	code := CodeOf(err)
	m.snapshot.LastFailure = code
	switch code {
	case CodeSenderForged, CodeAuthorization, CodeForeignTask, CodeAuthorityTransfer:
		m.snapshot.Denied[code]++
	case CodeVersionUnsupported, CodeEvidenceInvalid, CodeTooLarge, CodeInvalid:
		m.snapshot.Invalid[code]++
	default:
		m.snapshot.Errors[code]++
	}
}

func (m *Metrics) Snapshot() MetricsSnapshot {
	if m == nil {
		return MetricsSnapshot{}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	snapshot := m.snapshot
	snapshot.Denied = cloneMetricCodes(m.snapshot.Denied)
	snapshot.Invalid = cloneMetricCodes(m.snapshot.Invalid)
	snapshot.Errors = cloneMetricCodes(m.snapshot.Errors)
	return snapshot
}

func cloneMetricCodes(source map[ErrorCode]uint64) map[ErrorCode]uint64 {
	copy := make(map[ErrorCode]uint64, len(source))
	for code, count := range source {
		copy[code] = count
	}
	return copy
}
