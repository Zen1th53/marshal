package authz

import (
	"context"
	"fmt"

	"github.com/Zen1th53/marshal/internal/model"
)

type MemoryAction string

const (
	ActionMemoryRecall    MemoryAction = "memory.recall"
	ActionMemoryExpand    MemoryAction = "memory.expand"
	ActionMemoryRemember  MemoryAction = "memory.remember"
	ActionMemoryPromote   MemoryAction = "memory.promote"
	ActionMemoryCorrect   MemoryAction = "memory.correct"
	ActionMemorySupersede MemoryAction = "memory.supersede"
	ActionMemoryTombstone MemoryAction = "memory.tombstone"
	ActionMemorySnapshot  MemoryAction = "memory.snapshot"
	ActionMemoryBranch    MemoryAction = "memory.branch"
	ActionMemoryMerge     MemoryAction = "memory.merge"
)

var (
	ErrUnauthorized = fmt.Errorf("caller is not authorized for requested memory action")
)

type MemoryAuthorizer struct{}

func NewMemoryAuthorizer() *MemoryAuthorizer {
	return &MemoryAuthorizer{}
}

// Authorize evaluates caller principal permissions against semantic memory action and lifecycle.
func (a *MemoryAuthorizer) Authorize(ctx context.Context, principal Principal, action MemoryAction, scopeID string, targetLifecycle model.MemoryLifecycle) error {
	hasAuthority := func(req Authority) bool {
		for _, auth := range principal.Role.Authorities {
			if auth == req || auth == AuthorityPolicyAdmin {
				return true
			}
		}
		return false
	}

	switch action {
	case ActionMemoryRecall, ActionMemoryExpand:
		// Read operations require any valid agent authority
		if len(principal.Role.Authorities) == 0 {
			return ErrUnauthorized
		}
		return nil

	case ActionMemoryRemember, ActionMemoryCorrect, ActionMemorySnapshot, ActionMemoryBranch:
		// Candidate mutations / branch creations
		if hasAuthority(AuthoritySourceWrite) || hasAuthority(AuthorityTaskPlan) || hasAuthority(AuthorityPolicyAdmin) {
			return nil
		}
		return ErrUnauthorized

	case ActionMemoryPromote, ActionMemorySupersede, ActionMemoryTombstone, ActionMemoryMerge:
		// Privileged promotions to protected/durable memory or main branch merge
		if hasAuthority(AuthorityPolicyAdmin) || hasAuthority(AuthorityReleaseApprove) {
			return nil
		}
		return fmt.Errorf("%w: action %s requires operator policy admin authority", ErrUnauthorized, action)

	default:
		// Fail-closed on unknown actions
		return fmt.Errorf("%w: unknown memory action %q", ErrUnauthorized, action)
	}
}
