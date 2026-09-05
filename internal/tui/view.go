package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// UIState captures the complete snapshot of canonical runtime state needed for one-screen rendering.
type UIState struct {
	ProjectID          string
	SessionID          string
	SessionMode        string // "manual", "auto", "ULTRA"
	Goal               model.GoalContract
	UnderstandingState model.UnderstandingState
	TerminationState   model.TerminationState
	Participants       []model.Participant
	ActiveTurn         string
	Claims             []model.Claim
	RecentMessages     []model.AgentMessage
	BudgetConsumed     model.ConsumedBudget
	BudgetLimits       model.BudgetLimit
	ActiveQuestion     string
	ActiveBlocker      string
	RouteExplanation   string
}

// RenderScreen renders the full one-screen terminal workspace dashboard.
// Invariant: Minimum one-screen observability without scrolling; silence by default for agent chatter.
func RenderScreen(s UIState, width int) string {
	if width < 80 {
		width = 80
	}

	var b strings.Builder
	sep := strings.Repeat("─", width)
	doubleSep := strings.Repeat("═", width)

	// 1. Top Header
	modeBadge := fmt.Sprintf("[%s]", strings.ToUpper(s.SessionMode))
	if s.SessionMode == "" {
		modeBadge = "[ULTRA]"
	}
	stateBadge := fmt.Sprintf("[%s]", s.UnderstandingState)
	if s.TerminationState != "" {
		stateBadge = fmt.Sprintf("[%s / %s]", s.UnderstandingState, s.TerminationState)
	}

	b.WriteString(doubleSep + "\n")
	b.WriteString(fmt.Sprintf(" MARSHAL v1.5.0 CONTROL PLANE  │  Session: %s  │  Mode: %-8s  │  State: %s\n",
		truncate(s.SessionID, 16), modeBadge, stateBadge))
	b.WriteString(sep + "\n")

	// 2. Active Goal Summary
	outcome := s.Goal.DesiredOutcome
	if outcome == "" {
		outcome = "(No active goal set — use /goal <outcome> to initialize)"
	}
	b.WriteString(fmt.Sprintf(" GOAL [v%d]: %s\n", s.Goal.Revision, truncate(outcome, width-15)))
	if len(s.Goal.Constraints) > 0 {
		b.WriteString(fmt.Sprintf(" Active Constraints (%d): ", len(s.Goal.Constraints)))
		var cTexts []string
		for idx, c := range s.Goal.Constraints {
			if idx >= 3 {
				cTexts = append(cTexts, fmt.Sprintf("+%d more", len(s.Goal.Constraints)-3))
				break
			}
			cTexts = append(cTexts, truncate(c.Text, 25))
		}
		b.WriteString(strings.Join(cTexts, " │ ") + "\n")
	}
	b.WriteString(sep + "\n")

	// 3. Team Roster (Fixed Roles + Separate Harness & Model)
	b.WriteString(" TEAM ROSTER:\n")
	if len(s.Participants) == 0 {
		b.WriteString("   (No active participants)\n")
	} else {
		for _, p := range s.Participants {
			indicator := "  "
			if p.AgentID == s.ActiveTurn {
				indicator = "► "
			}
			status := "idle"
			if p.AgentID == s.ActiveTurn {
				status = "ACTIVE TURN"
			}
			b.WriteString(fmt.Sprintf("   %s%-16s │ Role: %-12s │ Harness: %-12s │ Model: %-18s │ %s\n",
				indicator, p.AgentID, p.Role, p.Harness, p.Model, status))
		}
	}
	b.WriteString(sep + "\n")

	// 4. Critical Claims & Epistemic Coverage
	verifiedCount := 0
	contestedCount := 0
	staleCount := 0
	supportedCount := 0
	for _, cl := range s.Claims {
		switch cl.State {
		case model.ClaimStateVerified:
			verifiedCount++
		case model.ClaimStateContested:
			contestedCount++
		case model.ClaimStateStale:
			staleCount++
		case model.ClaimStateSupported:
			supportedCount++
		}
	}
	b.WriteString(fmt.Sprintf(" CLAIMS (%d total) │ Verified: %d │ Supported: %d │ Contested: %d │ Stale: %d\n",
		len(s.Claims), verifiedCount, supportedCount, contestedCount, staleCount))

	// 5. Active Blocker / Decision / Concrete Question
	if s.ActiveQuestion != "" {
		b.WriteString(fmt.Sprintf(" ⚠️  DECISION NEEDED: %s\n", s.ActiveQuestion))
	} else if s.ActiveBlocker != "" {
		b.WriteString(fmt.Sprintf(" ⛔ BLOCKER: %s\n", s.ActiveBlocker))
	} else {
		b.WriteString(" ✓ No active blockers or pending operator questions\n")
	}
	b.WriteString(sep + "\n")

	// 6. Budget & Resource Usage
	tokStr := "unknown"
	if s.BudgetConsumed.TotalTokens != nil {
		tokStr = fmt.Sprintf("%d", *s.BudgetConsumed.TotalTokens)
	}
	costStr := "$0.00"
	if s.BudgetConsumed.CostUSD != nil {
		costStr = fmt.Sprintf("$%.4f", *s.BudgetConsumed.CostUSD)
	}
	b.WriteString(fmt.Sprintf(" BUDGET: Tokens: %-8s │ Cost: %-8s │ Calls: %-4d │ Handoffs: %-3d │ Time: %s\n",
		tokStr, costStr, s.BudgetConsumed.ModelCalls, s.BudgetConsumed.Handoffs, s.BudgetConsumed.Duration.Round(time.Millisecond)))
	b.WriteString(sep + "\n")

	// 7. Activity / Event Stream (Silence by default: collapse raw chatter)
	b.WriteString(" COLLABORATIVE ACTIVITY STREAM (Filtered):\n")
	meaningfulMsgs := filterMeaningfulMessages(s.RecentMessages, 5)
	if len(meaningfulMsgs) == 0 {
		b.WriteString("   (No significant handoffs or decisions recorded yet)\n")
	} else {
		for _, m := range meaningfulMsgs {
			b.WriteString(fmt.Sprintf("   [%s] %-12s ➜ %s\n",
				m.Kind, m.From.AgentID, truncate(m.Content, width-30)))
		}
	}

	// 8. Route / Harness Explanation (if present)
	if s.RouteExplanation != "" {
		b.WriteString(sep + "\n")
		b.WriteString(fmt.Sprintf(" ULTRA: %s\n", truncate(s.RouteExplanation, width-10)))
	}

	b.WriteString(doubleSep + "\n")
	return b.String()
}

func filterMeaningfulMessages(msgs []model.AgentMessage, limit int) []model.AgentMessage {
	var filtered []model.AgentMessage
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		// Silence by default: only surface findings, challenges, handoffs, verification requests
		if m.Kind == model.MessageFinding ||
			m.Kind == model.MessageClaimChallenge ||
			m.Kind == model.MessageHandoffProposal ||
			m.Kind == model.MessageVerificationRequest ||
			m.Kind == model.MessageFailedApproach {
			filtered = append([]model.AgentMessage{m}, filtered...)
			if len(filtered) >= limit {
				break
			}
		}
	}
	return filtered
}

func truncate(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	if max <= 3 {
		return s
	}
	if len(s) > max {
		return s[:max-3] + "..."
	}
	return s
}
