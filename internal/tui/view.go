package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// ToolExecutionCard captures real-time tool execution telemetry.
type ToolExecutionCard struct {
	ToolName    string
	Command     string
	Duration    time.Duration
	Status      string // RUNNING, SUCCESS, FAILED
	Summary     string
	Expanded    bool
	OutputLines []string
}

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
	GitStatus          GitStatusResult
	ActiveToolCard     *ToolExecutionCard
	KnownSecrets       []string
}

// RenderScreen renders the workspace screen with default theme for backward compatibility.
func RenderScreen(s UIState, width int) string {
	return RenderStyledScreen(s, NewTheme(ThemeDefault, true, true), width)
}

// RenderStyledScreen renders the dynamic MARSHAL workspace using the specified theme.
// Adheres strictly to:
// - Silence by default (collapsing noise, surfacing findings and decisions)
// - No static or fake data (only honest runtime values)
// - Responsive layout with semantic color and box-drawing borders
// - Dynamic one-screen observability
func RenderStyledScreen(s UIState, th *Theme, width int) string {
	if th == nil {
		th = NewTheme(ThemeDefault, true, true)
	}
	if width < 80 {
		width = 80
	}

	var b strings.Builder

	// 1. TOP DYNAMIC STATUS BAR
	// ╭─ MARSHAL ─ marshal ─ ULTRA ─ VERIFYING ────────── main · 123d960 · clean ─╮
	mode := strings.ToUpper(s.SessionMode)
	if mode == "" {
		mode = "ULTRA"
	}
	state := string(s.UnderstandingState)
	if state == "" {
		state = "READY"
	}
	if s.TerminationState != "" {
		state = fmt.Sprintf("%s / %s", state, s.TerminationState)
	}

	proj := s.ProjectID
	if proj == "" {
		proj = "marshal"
	}

	gitText := "git: detached"
	if s.GitStatus.Branch != "" && s.GitStatus.Branch != "unknown" {
		cleanText := "clean"
		if !s.GitStatus.Clean {
			cleanText = fmt.Sprintf("%d changed", s.GitStatus.ChangedCount)
		}
		gitText = fmt.Sprintf("%s · %s · %s", s.GitStatus.Branch, s.GitStatus.Commit, cleanText)
	}

	headerLeft := fmt.Sprintf(" %s %s %s %s %s %s %s ",
		th.Colorize(th.Marshal, "MARSHAL v1.5.0 CONTROL PLANE"),
		th.BoxHoriz,
		proj,
		th.BoxHoriz,
		th.Colorize(th.Ultra, fmt.Sprintf("[%s]", mode)),
		th.BoxHoriz,
		th.Colorize(th.Success, state),
	)
	headerRight := fmt.Sprintf(" %s ", th.Colorize(th.Muted, gitText))

	availWidth := width - VisibleLen(headerLeft) - VisibleLen(headerRight) - 2
	if availWidth < 0 {
		availWidth = 0
	}

	b.WriteString(fmt.Sprintf("%s%s%s%s%s%s\n",
		th.BoxTopLeft,
		headerLeft,
		strings.Repeat(th.BoxHoriz, availWidth),
		headerRight,
		th.BoxHoriz,
		th.BoxTopRight,
	))

	// Overview Sub-line: Goal % | Claims | Active Team | Budget | Risk
	verifiedCount, contestedCount, _, _ := countClaims(s.Claims)
	claimsDetailed := fmt.Sprintf("Claims (%d total) │ Verified: %d │ Contested: %d", len(s.Claims), verifiedCount, contestedCount)

	tokStr := "0"
	if s.BudgetConsumed.TotalTokens != nil {
		tokStr = fmt.Sprintf("%d", *s.BudgetConsumed.TotalTokens)
	}
	budgetCost := "$0.00"
	if s.BudgetConsumed.CostUSD != nil {
		budgetCost = fmt.Sprintf("$%.4f", *s.BudgetConsumed.CostUSD)
	}

	riskStr := "R1"
	if s.Goal.Risk != "" {
		riskStr = string(s.Goal.Risk)
	}

	activeCount := 0
	for _, p := range s.Participants {
		if p.IsActive {
			activeCount++
		}
	}

	overview := fmt.Sprintf(" %s  │  Tokens: %s  │  Cost: %s  │  Risk: %s",
		claimsDetailed,
		tokStr,
		budgetCost,
		th.Colorize(th.Warning, riskStr),
	)
	b.WriteString(fmt.Sprintf("%s %s%s\n",
		th.BoxVert,
		PadRight(overview, width-4),
		th.BoxVert,
	))

	// Split bar: ├─────────────────────────────────────────┤
	b.WriteString(fmt.Sprintf("%s%s%s\n",
		th.BoxTRight,
		strings.Repeat(th.BoxHoriz, width-2),
		th.BoxTLeft,
	))

	// 2. ACTIVE GOAL & CONSTRAINTS
	outcome := s.Goal.DesiredOutcome
	if outcome == "" {
		outcome = "(No active goal defined. Use /goal <desired outcome> to initialize)"
	}
	goalLine := fmt.Sprintf(" %s %s",
		th.Colorize(th.Bold, fmt.Sprintf("GOAL [v%d]:", s.Goal.Revision)),
		RedactContent(Truncate(outcome, width-16), s.KnownSecrets),
	)
	b.WriteString(fmt.Sprintf("%s %s%s\n", th.BoxVert, PadRight(goalLine, width-4), th.BoxVert))

	if len(s.Goal.Constraints) > 0 {
		var cList []string
		for idx, c := range s.Goal.Constraints {
			if idx >= 3 {
				cList = append(cList, fmt.Sprintf("+%d more", len(s.Goal.Constraints)-3))
				break
			}
			cList = append(cList, fmt.Sprintf("%s %s", th.GlyphCheck, Truncate(c.Text, 24)))
		}
		cLine := fmt.Sprintf("   Constraints: %s", strings.Join(cList, " │ "))
		b.WriteString(fmt.Sprintf("%s %s%s\n", th.BoxVert, PadRight(cLine, width-4), th.BoxVert))
	}

	// Separator
	b.WriteString(fmt.Sprintf("%s%s%s\n",
		th.BoxTRight,
		strings.Repeat(th.BoxHoriz, width-2),
		th.BoxTLeft,
	))

	// 3. TEAM & WORKSPACE SPLIT (Dual Pane representation)
	teamHeader := fmt.Sprintf(" %s", th.Colorize(th.Bold, "ACTIVE TEAM ROSTER:"))
	b.WriteString(fmt.Sprintf("%s %s%s\n", th.BoxVert, PadRight(teamHeader, width-4), th.BoxVert))

	if len(s.Participants) == 0 {
		b.WriteString(fmt.Sprintf("%s %s%s\n",
			th.BoxVert,
			PadRight("   (No active participants — use /agents add or /harness probe)", width-4),
			th.BoxVert,
		))
	} else {
		for _, p := range s.Participants {
			var stateGlyph string
			var statusText string
			if p.AgentID == s.ActiveTurn {
				stateGlyph = th.Colorize(th.Active, th.GlyphArrowR)
				statusText = th.Colorize(th.Success, "WORKING")
			} else if p.IsActive {
				stateGlyph = th.Colorize(th.Success, th.GlyphDotFull)
				statusText = th.Colorize(th.Muted, "IDLE")
			} else {
				stateGlyph = th.Colorize(th.Danger, th.GlyphCross)
				statusText = th.Colorize(th.Danger, "UNAVAILABLE")
			}

			modelText := p.Model
			if modelText == "" || modelText == "UNKNOWN" {
				modelText = th.Colorize(th.Muted, "UNKNOWN")
			}

			agentRow := fmt.Sprintf("   %s %-12s  Role: %-10s  Harness: %-12s  Model: %-16s  [%s]",
				stateGlyph,
				th.Colorize(th.Bold, p.AgentID),
				p.Role,
				p.Harness,
				modelText,
				statusText,
			)
			b.WriteString(fmt.Sprintf("%s %s%s\n", th.BoxVert, PadRight(agentRow, width-4), th.BoxVert))
		}
	}

	// Separator
	b.WriteString(fmt.Sprintf("%s%s%s\n",
		th.BoxTRight,
		strings.Repeat(th.BoxHoriz, width-2),
		th.BoxTLeft,
	))

	// 4. BLOCKERS / DECISIONS / NOTICES
	if s.ActiveQuestion != "" {
		notice := fmt.Sprintf(" %s DECISION REQUIRED: %s",
			th.Colorize(th.Warning, "⚠"),
			RedactContent(s.ActiveQuestion, s.KnownSecrets),
		)
		b.WriteString(fmt.Sprintf("%s %s%s\n", th.BoxVert, PadRight(notice, width-4), th.BoxVert))
		b.WriteString(fmt.Sprintf("%s%s%s\n", th.BoxTRight, strings.Repeat(th.BoxHoriz, width-2), th.BoxTLeft))
	} else if s.ActiveBlocker != "" {
		notice := fmt.Sprintf(" %s BLOCKER: %s",
			th.Colorize(th.Danger, "⛔"),
			RedactContent(s.ActiveBlocker, s.KnownSecrets),
		)
		b.WriteString(fmt.Sprintf("%s %s%s\n", th.BoxVert, PadRight(notice, width-4), th.BoxVert))
		b.WriteString(fmt.Sprintf("%s%s%s\n", th.BoxTRight, strings.Repeat(th.BoxHoriz, width-2), th.BoxTLeft))
	}

	// 5. TOOL EXECUTION CARD (Compact live card if active)
	if s.ActiveToolCard != nil {
		card := s.ActiveToolCard
		durationStr := fmt.Sprintf("%.1fs", card.Duration.Seconds())
		cardHeader := fmt.Sprintf(" TOOL: %s [%s]  (%s)",
			th.Colorize(th.Bold, card.ToolName),
			card.Status,
			durationStr,
		)
		b.WriteString(fmt.Sprintf("%s %s%s\n", th.BoxVert, PadRight(cardHeader, width-4), th.BoxVert))
		if card.Summary != "" {
			cardSummary := fmt.Sprintf("   Command: %s", RedactContent(Truncate(card.Summary, width-16), s.KnownSecrets))
			b.WriteString(fmt.Sprintf("%s %s%s\n", th.BoxVert, PadRight(cardSummary, width-4), th.BoxVert))
		}
		b.WriteString(fmt.Sprintf("%s%s%s\n", th.BoxTRight, strings.Repeat(th.BoxHoriz, width-2), th.BoxTLeft))
	}

	// 6. COLLABORATIVE ACTIVITY STREAM (Filtered, Silence-by-Default)
	activityTitle := fmt.Sprintf(" %s", th.Colorize(th.Bold, "COLLABORATIVE ACTIVITY (Filtered Findings & Handoffs):"))
	b.WriteString(fmt.Sprintf("%s %s%s\n", th.BoxVert, PadRight(activityTitle, width-4), th.BoxVert))

	meaningfulMsgs := filterMeaningfulMessages(s.RecentMessages, 4)
	if len(meaningfulMsgs) == 0 {
		emptyMsg := "   (No significant findings, challenges, or handoffs recorded yet)"
		b.WriteString(fmt.Sprintf("%s %s%s\n", th.BoxVert, PadRight(emptyMsg, width-4), th.BoxVert))
	} else {
		for _, m := range meaningfulMsgs {
			kindBadge := fmt.Sprintf("[%s]", m.Kind)
			agentName := strings.ToUpper(m.From.AgentID)
			content := RedactContent(Truncate(m.Content, width-VisibleLen(kindBadge)-VisibleLen(agentName)-16), s.KnownSecrets)
			row := fmt.Sprintf("   %s %s %s %s",
				th.Colorize(th.Accent, kindBadge),
				th.Colorize(th.Bold, agentName),
				th.GlyphArrowR,
				content,
			)
			b.WriteString(fmt.Sprintf("%s %s%s\n", th.BoxVert, PadRight(row, width-4), th.BoxVert))
		}
	}

	// 7. ROUTING / ULTRA EXPLANATION (if present)
	if s.RouteExplanation != "" {
		b.WriteString(fmt.Sprintf("%s%s%s\n",
			th.BoxTRight,
			strings.Repeat(th.BoxHoriz, width-2),
			th.BoxTLeft,
		))
		ultraText := fmt.Sprintf(" %s: %s",
			th.Colorize(th.Ultra, "ULTRA ROUTE"),
			RedactContent(Truncate(s.RouteExplanation, width-18), s.KnownSecrets),
		)
		b.WriteString(fmt.Sprintf("%s %s%s\n", th.BoxVert, PadRight(ultraText, width-4), th.BoxVert))
	}

	// 8. FOOTER: Keybindings help
	footerShortcuts := " [Ctrl+P] Palette  [Tab] Complete  [d] Diff  [/] Commands  [@] Agents  [?] Help "
	remFooter := width - VisibleLen(footerShortcuts) - 2
	if remFooter < 0 {
		remFooter = 0
	}
	b.WriteString(fmt.Sprintf("%s%s%s%s%s\n",
		th.BoxBottomLeft,
		th.BoxHoriz,
		th.Colorize(th.Muted, footerShortcuts),
		strings.Repeat(th.BoxHoriz, remFooter),
		th.BoxBottomRight,
	))

	return b.String()
}

func countClaims(claims []model.Claim) (verified, contested, supported, stale int) {
	for _, c := range claims {
		switch c.State {
		case model.ClaimStateVerified:
			verified++
		case model.ClaimStateContested:
			contested++
		case model.ClaimStateSupported:
			supported++
		case model.ClaimStateStale:
			stale++
		}
	}
	return
}

func summarizeClaims(claims []model.Claim) string {
	if len(claims) == 0 {
		return "0/0"
	}
	verified := 0
	contested := 0
	for _, c := range claims {
		if c.State == model.ClaimStateVerified {
			verified++
		} else if c.State == model.ClaimStateContested {
			contested++
		}
	}
	if contested > 0 {
		return fmt.Sprintf("%d/%d (⚡%d)", verified, len(claims), contested)
	}
	return fmt.Sprintf("%d/%d", verified, len(claims))
}

func filterMeaningfulMessages(msgs []model.AgentMessage, limit int) []model.AgentMessage {
	var filtered []model.AgentMessage
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
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

