package adaptive

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrUnsafeLearnedProposal = errors.New("learned memory policy proposed action violated deterministic safety envelope")
)

type LearnedPolicy interface {
	ProposeAction(ctx context.Context, state TaskState) (MemoryAction, string)
}

type ShadowDiffReport struct {
	TaskID         string       `json:"task_id"`
	ShadowMode     bool         `json:"shadow_mode"`
	ActiveAction   MemoryAction `json:"active_action"`
	ProposedAction MemoryAction `json:"proposed_action"`
	Fingerprint    string       `json:"fingerprint"`
	Timestamp      time.Time    `json:"timestamp"`
}

type SafetyEnvelope struct {
	mu         sync.RWMutex
	policy     LearnedPolicy
	shadowMode bool
	diffs      []ShadowDiffReport
}

func NewSafetyEnvelope(policy LearnedPolicy, shadowMode bool) *SafetyEnvelope {
	return &SafetyEnvelope{
		policy:     policy,
		shadowMode: shadowMode,
	}
}

// IsAllowedAction checks whether the proposed action is within the permitted safe vocabulary.
func IsAllowedAction(act ActionType) bool {
	switch act {
	case ActionNoOp, ActionRecall, ActionReQuery, ActionExpand, ActionNavigate, ActionInjectProcedure, ActionConsolidate:
		return true
	}
	return false
}

// ExecuteProposal evaluates a learned proposal through deterministic safety and authorization constraints.
func (e *SafetyEnvelope) ExecuteProposal(ctx context.Context, state TaskState) (MemoryAction, error) {
	if e.policy == nil {
		return MemoryAction{Type: ActionNoOp, Reason: "no learned policy installed"}, nil
	}

	proposal, fingerprint := e.policy.ProposeAction(ctx, state)

	if !IsAllowedAction(proposal.Type) {
		return MemoryAction{}, fmt.Errorf("%w: invalid action %s from fingerprint %s", ErrUnsafeLearnedProposal, proposal.Type, fingerprint)
	}

	return proposal, nil
}

// ExecuteShadow compares the learned policy proposal against the deterministic active action without mutating runtime state.
func (e *SafetyEnvelope) ExecuteShadow(ctx context.Context, state TaskState, activeAction MemoryAction) (ShadowDiffReport, error) {
	if e.policy == nil {
		return ShadowDiffReport{}, errors.New("no learned policy installed")
	}

	proposal, fingerprint := e.policy.ProposeAction(ctx, state)

	diff := ShadowDiffReport{
		TaskID:         state.TaskID,
		ShadowMode:     true,
		ActiveAction:   activeAction,
		ProposedAction: proposal,
		Fingerprint:    fingerprint,
		Timestamp:      time.Now().UTC(),
	}

	e.mu.Lock()
	e.diffs = append(e.diffs, diff)
	e.mu.Unlock()

	return diff, nil
}
