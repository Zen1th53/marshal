package retention

import (
	"context"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

type PolicyConfig struct {
	WorkingMemoryTTL time.Duration
}

type PolicyEvaluator struct {
	config PolicyConfig
}

func NewPolicyEvaluator(config PolicyConfig) *PolicyEvaluator {
	if config.WorkingMemoryTTL == 0 {
		config.WorkingMemoryTTL = 24 * time.Hour
	}
	return &PolicyEvaluator{config: config}
}

// CheckStaleness checks if a memory record has become stale due to commit drift or TTL expiration.
func (p *PolicyEvaluator) CheckStaleness(ctx context.Context, rec model.MemoryRecordV2, currentHeadCommit string, now time.Time) (bool, string) {
	// 1. Commit drift: if record was explicitly bound to a commit that has moved
	if rec.HeadCommit != "" && currentHeadCommit != "" && rec.HeadCommit != currentHeadCommit {
		return true, fmt.Sprintf("referenced commit changed: bound %s, current %s", rec.HeadCommit, currentHeadCommit)
	}

	// 2. Working memory TTL
	if rec.Kind == model.MemoryKindWorking {
		age := now.Sub(rec.ObservedAt)
		if age > p.config.WorkingMemoryTTL {
			return true, fmt.Sprintf("working memory exceeded TTL (age: %v, ttl: %v)", age, p.config.WorkingMemoryTTL)
		}
	}

	return false, ""
}

// TombstoneRecord prepares a memory record for tombstone deletion.
// If purgeBody is true, the body payload is wiped clean to prevent residual leakage.
func (p *PolicyEvaluator) TombstoneRecord(rec model.MemoryRecordV2, reason string, purgeBody bool) model.MemoryRecordV2 {
	tombstoned := rec
	tombstoned.Lifecycle = model.MemoryTombstoned
	tombstoned.UpdatedAt = time.Now().UTC()

	if purgeBody {
		tombstoned.Body = "[PURGED]"
	}

	if tombstoned.ExtMeta == nil {
		tombstoned.ExtMeta = make(map[string]any)
	}
	tombstoned.ExtMeta["tombstone_reason"] = reason

	tombstoned.ContentDigest = tombstoned.CanonicalDigest()
	return tombstoned
}
