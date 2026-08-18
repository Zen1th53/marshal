package adapter

import (
	"context"
	"fmt"

	"github.com/Zen1th53/marshal/internal/context/budget"
)

type BudgetGovernor struct {
	manager *budget.Manager
}

func NewBudgetGovernor(manager *budget.Manager) *BudgetGovernor {
	return &BudgetGovernor{manager: manager}
}

func (g *BudgetGovernor) ManageBudget(ctx context.Context, maxTokens int, sections []budget.SectionPriority) (*budget.Decision, error) {
	if g == nil || g.manager == nil {
		return nil, fmt.Errorf("budget governor uninitialized")
	}
	return g.manager.Allocate(ctx, budget.Budget{MaxTokens: maxTokens, ReserveTokens: 100}, sections)
}
