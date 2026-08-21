package lifecycle

import (
	"context"
	"errors"
	"fmt"

	"github.com/Zen1th53/marshal/internal/model"
)

var (
	ErrInvalidTransition     = errors.New("invalid memory lifecycle transition")
	ErrInsufficientAuthority = errors.New("insufficient authority for memory promotion")
	ErrTerminalLifecycle     = errors.New("record is in a terminal lifecycle state and cannot be reactivated")
)

// StateMachine enforces canonical memory lifecycle transitions and promotion authority.
type StateMachine struct{}

func NewStateMachine() *StateMachine {
	return &StateMachine{}
}

// Transition evaluates whether current record can transition to target lifecycle
// under the actor's authority class, and returns the updated record if allowed.
func (sm *StateMachine) Transition(ctx context.Context, current model.MemoryRecordV2, target model.MemoryLifecycle, actorAuthority model.MemoryAuthority) (model.MemoryRecordV2, error) {
	if !target.IsValid() {
		return model.MemoryRecordV2{}, fmt.Errorf("%w: target lifecycle %q is invalid", ErrInvalidTransition, target)
	}

	from := current.Lifecycle

	// 1. Terminal states check
	if from == model.MemoryTombstoned || from == model.MemoryRejected {
		return model.MemoryRecordV2{}, fmt.Errorf("%w: cannot transition from %s to %s", ErrTerminalLifecycle, from, target)
	}

	// 2. Transition matrix check
	if !isLegalTransition(from, target) {
		return model.MemoryRecordV2{}, fmt.Errorf("%w: cannot transition from %s to %s", ErrInvalidTransition, from, target)
	}

	// 3. Authority checks
	// Promoting to Verified requires at least AuthorityVerified
	if target == model.MemoryVerified {
		if actorAuthority != model.AuthorityVerified && actorAuthority != model.AuthorityPolicy && actorAuthority != model.AuthorityOperator {
			return model.MemoryRecordV2{}, fmt.Errorf("%w: transitioning to %s requires at least verified authority", ErrInsufficientAuthority, target)
		}
	}

	// Promoting to Durable requires authority depending on kind
	if target == model.MemoryDurable {
		// Protected kinds: decision, finding, failure require Policy or Operator authority
		if current.Kind == model.MemoryKindDecision || current.Kind == model.MemoryKindFinding || current.Kind == model.MemoryKindFailure {
			if actorAuthority != model.AuthorityPolicy && actorAuthority != model.AuthorityOperator {
				return model.MemoryRecordV2{}, fmt.Errorf("%w: promoting %s to %s requires policy or operator authority", ErrInsufficientAuthority, current.Kind, target)
			}
		} else {
			// General kinds require at least verified authority
			if actorAuthority == model.AuthorityAgent {
				return model.MemoryRecordV2{}, fmt.Errorf("%w: single agent cannot self-promote to durable", ErrInsufficientAuthority)
			}
		}
	}

	updated := current
	updated.Lifecycle = target
	if actorAuthority != "" {
		updated.Authority = actorAuthority
	}

	return updated, nil
}

func isLegalTransition(from, to model.MemoryLifecycle) bool {
	if from == to {
		return true
	}

	switch from {
	case model.MemoryObserved:
		return to == model.MemoryCandidate || to == model.MemoryRejected || to == model.MemoryTombstoned
	case model.MemoryCandidate:
		return to == model.MemoryVerified || to == model.MemoryRejected || to == model.MemoryTombstoned
	case model.MemoryVerified:
		return to == model.MemoryDurable || to == model.MemoryRejected || to == model.MemoryTombstoned || to == model.MemoryConflicted
	case model.MemoryDurable:
		return to == model.MemoryStale || to == model.MemoryConflicted || to == model.MemorySuperseded || to == model.MemoryTombstoned
	case model.MemoryStale:
		// Stale can be re-evaluated as candidate or re-verified, but not directly jump to durable
		return to == model.MemoryCandidate || to == model.MemoryVerified || to == model.MemoryTombstoned || to == model.MemorySuperseded
	case model.MemoryConflicted:
		return to == model.MemoryCandidate || to == model.MemoryVerified || to == model.MemorySuperseded || to == model.MemoryTombstoned
	case model.MemorySuperseded:
		return to == model.MemoryTombstoned
	default:
		return false
	}
}
