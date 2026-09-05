package reinjection

import (
	"context"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/protocol"
)

// Engine is the central orchestrator for Core #6 Constraint Re-injection.
type Engine struct {
	compiler *ConstraintCompiler
	guard    *ConstraintGuard
}

func NewEngine() *Engine {
	return &Engine{
		compiler: NewConstraintCompiler(),
		guard:    NewConstraintGuard(),
	}
}

func (e *Engine) Compiler() *ConstraintCompiler {
	return e.compiler
}

func (e *Engine) Guard() *ConstraintGuard {
	return e.guard
}

// PrepareTaskContext compiles the active Goal constraints and re-injects them
// into the execution prompt for the recipient principal.
func (e *Engine) PrepareTaskContext(
	ctx context.Context,
	goal model.GoalContract,
	recipient protocol.Principal,
	basePrompt string,
) (string, CompiledConstraints, error) {
	compiled, err := e.compiler.Compile(ctx, goal, recipient)
	if err != nil {
		return "", CompiledConstraints{}, err
	}

	fullPrompt := e.compiler.InjectIntoPrompt(basePrompt, compiled)
	return fullPrompt, compiled, nil
}

// BuildHandoff constructs a typed protocol.Handoff with authoritative constraint references
// and digest extracted directly from the active Goal.
func (e *Engine) BuildHandoff(
	ctx context.Context,
	goal model.GoalContract,
	fromAgent string,
	toRole protocol.Role,
	taskID protocol.TaskID,
	claims map[string]string,
	evidenceIDs []protocol.EvidenceID,
	changedFiles []string,
	risks []string,
	unresolved []string,
	idempotencyKey string,
) (protocol.Handoff, error) {
	refs, digest := ExtractConstraintRefs(goal)

	now := time.Now().UTC()
	hID, err := model.NewID("HO-")
	if err != nil {
		return protocol.Handoff{}, err
	}

	handoff := protocol.Handoff{
		ID:                protocol.HandoffID(hID),
		Version:           protocol.Version1,
		TaskID:            taskID,
		FromAgent:         fromAgent,
		ToRole:            toRole,
		Status:            protocol.StatusCreated,
		Claims:            claims,
		EvidenceIDs:       evidenceIDs,
		ChangedFiles:      changedFiles,
		Risks:             risks,
		Unresolved:        unresolved,
		ConstraintRefs:    refs,
		ConstraintsDigest: digest,
		ContextDigest:     digest, // Bound to constraint context digest
		CreatedAt:         now,
		IdempotencyKey:    idempotencyKey,
	}

	if err := handoff.Validate(); err != nil {
		return protocol.Handoff{}, err
	}

	return handoff, nil
}

// ValidateHandoff verifies that an incoming handoff preserves active Goal constraints.
func (e *Engine) ValidateHandoff(ctx context.Context, goal model.GoalContract, handoff protocol.Handoff) error {
	return e.guard.ValidateHandoff(goal, handoff)
}

// CompactContext protects authoritative constraints from being lost during LLM context compaction.
func (e *Engine) CompactContext(
	ctx context.Context,
	priorPrompt string,
	summary string,
	goal model.GoalContract,
	recipient protocol.Principal,
) (string, error) {
	compiled, err := e.compiler.Compile(ctx, goal, recipient)
	if err != nil {
		return "", err
	}

	return e.compiler.CompactWithConstraints(priorPrompt, summary, compiled), nil
}
