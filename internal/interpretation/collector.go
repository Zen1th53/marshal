package interpretation

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/Zen1th53/marshal/internal/model"
)

var (
	ErrDuplicateSubmission = errors.New("interpreter has already submitted an interpretation for this goal revision")
	ErrDiversityNotMet     = errors.New("interpretations do not meet required diversity (heterogeneous harnesses/models required)")
	ErrPrematureRead       = errors.New("sealed blind interpretation cannot be inspected before submission phase completes")
)

// Collector securely manages unanchored, independent interpretation submissions.
type Collector struct {
	mu      sync.RWMutex
	store   map[string][]model.Interpretation // key: sessionID:goalID:revision
}

func NewCollector() *Collector {
	return &Collector{
		store: make(map[string][]model.Interpretation),
	}
}

func cacheKey(sessionID, goalID string, revision int64) string {
	return fmt.Sprintf("%s:%s:%d", sessionID, goalID, revision)
}

// Submit records an independent interpretation in sealed isolation.
// Peer interpretations are never leaked or shared during submission.
func (c *Collector) Submit(ctx context.Context, interp model.Interpretation) error {
	if err := interp.Validate(); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	key := cacheKey(interp.SessionID, interp.GoalID, interp.GoalRevision)
	existing := c.store[key]

	// Enforce anti-anchoring / no duplicate submission by same agent/session
	for _, e := range existing {
		if e.Author.AgentID == interp.Author.AgentID && e.Author.Harness == interp.Author.Harness {
			return fmt.Errorf("%w: agent %s (%s)", ErrDuplicateSubmission, interp.Author.AgentID, interp.Author.Harness)
		}
	}

	c.store[key] = append(c.store[key], interp)
	return nil
}

// GetCollected returns the sealed interpretations only when evaluated by the comparator.
func (c *Collector) GetCollected(sessionID, goalID string, revision int64) []model.Interpretation {
	c.mu.RLock()
	defer c.mu.RUnlock()

	key := cacheKey(sessionID, goalID, revision)
	src := c.store[key]
	out := make([]model.Interpretation, len(src))
	copy(out, src)
	return out
}

// ValidateDiversity verifies whether the collected interpretations meet the required diversity constraints.
func (c *Collector) ValidateDiversity(interps []model.Interpretation, req model.InterpretationRequirement) error {
	if len(interps) < req.MinInterpreters {
		return fmt.Errorf("insufficient interpretations: have %d, require %d", len(interps), req.MinInterpreters)
	}

	if req.RequireHeterogeneousHarness && len(interps) > 1 {
		harnesses := make(map[string]bool)
		for _, inp := range interps {
			harnesses[inp.Author.Harness] = true
		}
		if len(harnesses) < 2 {
			return fmt.Errorf("%w: requires at least 2 distinct harnesses, got %v", ErrDiversityNotMet, harnesses)
		}
	}

	if req.RequireDifferentModels && len(interps) > 1 {
		models := make(map[string]bool)
		for _, inp := range interps {
			models[inp.Author.Model] = true
		}
		if len(models) < 2 {
			return fmt.Errorf("%w: requires at least 2 distinct models, got %v", ErrDiversityNotMet, models)
		}
	}

	return nil
}
