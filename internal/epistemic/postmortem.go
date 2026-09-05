package epistemic

import (
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// PostMortemCard contains concise, structured retrospective data on a finished task or goal.
type PostMortemCard struct {
	GoalID          string                `json:"goal_id"`
	GoalRevision    int64                 `json:"goal_revision"`
	WhatWorked      []string              `json:"what_worked"`
	WhatWasRedone   []string              `json:"what_was_redone"`
	WrongAssumptions []string             `json:"wrong_assumptions"`
	Failures        []string              `json:"failures"`
	ConsumedBudget  model.ConsumedBudget  `json:"consumed_budget"`
	UnresolvedRisks []string              `json:"unresolved_risks"`
	RoutingLessons  []string              `json:"routing_lessons"`
	GeneratedAt     time.Time             `json:"generated_at"`
}

// GeneratePostMortemCard produces a concise markdown summary for developers and operators.
func GeneratePostMortemCard(c PostMortemCard) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Post-Mortem Card: Goal %s (rev %d)\n\n", c.GoalID, c.GoalRevision))
	sb.WriteString(fmt.Sprintf("**Generated At:** %s\n\n", c.GeneratedAt.UTC().Format(time.RFC3339)))

	sb.WriteString("## 1. What Worked\n")
	if len(c.WhatWorked) == 0 {
		sb.WriteString("- None recorded\n")
	} else {
		for _, w := range c.WhatWorked {
			sb.WriteString(fmt.Sprintf("- %s\n", w))
		}
	}

	sb.WriteString("\n## 2. What Was Redone / Rolled Back\n")
	if len(c.WhatWasRedone) == 0 {
		sb.WriteString("- Zero rework required\n")
	} else {
		for _, r := range c.WhatWasRedone {
			sb.WriteString(fmt.Sprintf("- %s\n", r))
		}
	}

	sb.WriteString("\n## 3. Invalidated / Wrong Assumptions\n")
	if len(c.WrongAssumptions) == 0 {
		sb.WriteString("- All initial assumptions held\n")
	} else {
		for _, a := range c.WrongAssumptions {
			sb.WriteString(fmt.Sprintf("- %s\n", a))
		}
	}

	sb.WriteString("\n## 4. Failures & Fingerprints\n")
	if len(c.Failures) == 0 {
		sb.WriteString("- No notable failures recorded\n")
	} else {
		for _, f := range c.Failures {
			sb.WriteString(fmt.Sprintf("- %s\n", f))
		}
	}

	sb.WriteString("\n## 5. Budget & Resource Usage\n")
	tokStr := "unknown"
	if c.ConsumedBudget.TotalTokens != nil {
		tokStr = fmt.Sprintf("%d tokens", *c.ConsumedBudget.TotalTokens)
	}
	costStr := "$0.00"
	if c.ConsumedBudget.CostUSD != nil {
		costStr = fmt.Sprintf("$%.4f", *c.ConsumedBudget.CostUSD)
	}
	sb.WriteString(fmt.Sprintf("- **Tokens:** %s\n", tokStr))
	sb.WriteString(fmt.Sprintf("- **Cost:** %s\n", costStr))
	sb.WriteString(fmt.Sprintf("- **Model Calls:** %d\n", c.ConsumedBudget.ModelCalls))
	sb.WriteString(fmt.Sprintf("- **Handoffs:** %d\n", c.ConsumedBudget.Handoffs))
	sb.WriteString(fmt.Sprintf("- **Duration:** %s\n", c.ConsumedBudget.Duration.Round(time.Millisecond)))

	sb.WriteString("\n## 6. Unresolved Risks\n")
	if len(c.UnresolvedRisks) == 0 {
		sb.WriteString("- None\n")
	} else {
		for _, ur := range c.UnresolvedRisks {
			sb.WriteString(fmt.Sprintf("- %s\n", ur))
		}
	}

	sb.WriteString("\n## 7. Routing Lessons\n")
	if len(c.RoutingLessons) == 0 {
		sb.WriteString("- Default routes optimal\n")
	} else {
		for _, rl := range c.RoutingLessons {
			sb.WriteString(fmt.Sprintf("- %s\n", rl))
		}
	}

	return sb.String()
}
