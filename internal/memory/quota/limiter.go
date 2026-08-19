package quota

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrQuotaExceeded         = errors.New("memory write rate quota exceeded")
	ErrStorageBudgetExceeded = errors.New("project storage byte budget exceeded")
	ErrImportBatchTooLarge   = errors.New("import batch size exceeds maximum allowed limit")
	ErrBacklogFull           = errors.New("memory index queue backlog full")
)

type Config struct {
	MaxWritesPerMinute int
	MaxStorageBytes    int64
	MaxImportBatchSize int
	MaxRecallTopK      int
}

type Limiter struct {
	config       Config
	mu           sync.Mutex
	writes       map[string][]time.Time // key: projectID
	storageBytes map[string]int64       // key: projectID
}

func NewLimiter(config Config) *Limiter {
	if config.MaxWritesPerMinute <= 0 {
		config.MaxWritesPerMinute = 60
	}
	if config.MaxStorageBytes <= 0 {
		config.MaxStorageBytes = 50 * 1024 * 1024 // 50MB
	}
	if config.MaxImportBatchSize <= 0 {
		config.MaxImportBatchSize = 500
	}
	if config.MaxRecallTopK <= 0 {
		config.MaxRecallTopK = 50
	}

	return &Limiter{
		config:       config,
		writes:       make(map[string][]time.Time),
		storageBytes: make(map[string]int64),
	}
}

// AllowWrite verifies rate limit and storage capacity before admitting a memory record.
func (l *Limiter) AllowWrite(ctx context.Context, projectID string, payloadBytes int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now().UTC()
	cutoff := now.Add(-1 * time.Minute)

	var active []time.Time
	for _, t := range l.writes[projectID] {
		if t.After(cutoff) {
			active = append(active, t)
		}
	}

	if len(active) >= l.config.MaxWritesPerMinute {
		return fmt.Errorf("%w: project %s hit limit of %d writes/min", ErrQuotaExceeded, projectID, l.config.MaxWritesPerMinute)
	}

	currentStorage := l.storageBytes[projectID]
	if currentStorage+payloadBytes > l.config.MaxStorageBytes {
		return fmt.Errorf("%w: project %s storage %d bytes exceeds budget %d bytes", ErrStorageBudgetExceeded, projectID, currentStorage+payloadBytes, l.config.MaxStorageBytes)
	}

	active = append(active, now)
	l.writes[projectID] = active
	l.storageBytes[projectID] += payloadBytes

	return nil
}

// ValidateImportBatch ensures batch size does not exceed DoS thresholds.
func (l *Limiter) ValidateImportBatch(ctx context.Context, batchSize int) error {
	if batchSize > l.config.MaxImportBatchSize {
		return fmt.Errorf("%w: batch size %d > %d", ErrImportBatchTooLarge, batchSize, l.config.MaxImportBatchSize)
	}
	return nil
}

// ClampRecallK bounds search fan-out to configured safety maximum.
func (l *Limiter) ClampRecallK(requestedK int) int {
	if requestedK <= 0 || requestedK > l.config.MaxRecallTopK {
		return l.config.MaxRecallTopK
	}
	return requestedK
}
