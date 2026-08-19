package utility

import (
	"context"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

type UsageOutcome struct {
	MemoryID         string    `json:"memory_id"`
	TaskID           string    `json:"task_id"`
	Success          bool      `json:"success"`
	OperatorApproved bool      `json:"operator_approved"`
	Timestamp        time.Time `json:"timestamp"`
}

type MemoryUtilityState struct {
	SuccessCount int       `json:"success_count"`
	FailureCount int       `json:"failure_count"`
	Score        float64   `json:"score"`
	LastUsedAt   time.Time `json:"last_used_at"`
}

type Ledger struct {
	mu     sync.RWMutex
	states map[string]MemoryUtilityState
}

func NewLedger() *Ledger {
	return &Ledger{
		states: make(map[string]MemoryUtilityState),
	}
}

// RecordOutcome logs task execution outcome associated with a memory ID.
func (l *Ledger) RecordOutcome(ctx context.Context, memoryID, taskID string, success, operatorApproved bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	st := l.states[memoryID]
	if success {
		st.SuccessCount++
	} else {
		st.FailureCount++
	}
	st.LastUsedAt = time.Now().UTC()

	// Laplace smoothed utility score: (1 + success) / (2 + success + failure)
	st.Score = float64(1+st.SuccessCount) / float64(2+st.SuccessCount+st.FailureCount)
	l.states[memoryID] = st
}

// GetUtilityScore returns the current bounded utility score for a memory ID.
func (l *Ledger) GetUtilityScore(ctx context.Context, memoryID string) float64 {
	l.mu.RLock()
	defer l.mu.RUnlock()

	st, ok := l.states[memoryID]
	if !ok {
		return 0.5 // Neutral prior
	}
	return st.Score
}

// CalculateRankBoost computes final ranking weight where authority strictly dominates utility.
func (l *Ledger) CalculateRankBoost(rec model.MemoryRecordV2, utilityScore float64) float64 {
	var authorityBase float64
	switch rec.Authority {
	case model.AuthorityOperator:
		authorityBase = 10.0
	case model.AuthorityPolicy:
		authorityBase = 8.0
	case model.AuthorityVerified:
		authorityBase = 6.0
	case model.AuthorityAgent:
		authorityBase = 2.0
	default:
		authorityBase = 1.0
	}

	// Utility adds bounded micro-adjustment within authority tier
	return authorityBase + (utilityScore * 0.5)
}
