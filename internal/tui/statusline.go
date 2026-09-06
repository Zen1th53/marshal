package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Zen1th53/marshal/internal/model"
)

// StatusSegment is one field of the statusline, carrying the priority that
// decides whether it survives on a narrow terminal.
type StatusSegment struct {
	Text     string // rendered, may carry colour
	Priority int    // 1 is most important and is dropped last
}

// RenderStatusline builds the single persistent status row.
//
// Fields are dropped by descending priority until the row fits the terminal, so
// a narrow window keeps the identity and git state rather than truncating every
// field into uselessness. Exactly one of these exists on screen; it is repainted
// in place rather than reprinted.
func RenderStatusline(s UIState, th *Theme, workDir string, width int) string {
	if th == nil {
		th = NewTheme(ThemeDefault, true, true)
	}
	if width < 20 {
		width = 20
	}

	segments := []StatusSegment{
		{Text: th.Colorize(th.Marshal, ProjectLabel(s, workDir, 32)), Priority: 1},
		{Text: gitSegment(s, th), Priority: 2},
		{Text: th.Colorize(th.Ultra, strings.ToUpper(orDefault(s.SessionMode, "ULTRA"))), Priority: 3},
		{Text: runtimeStateSegment(s, th), Priority: 4},
		{Text: agentSegment(s, th), Priority: 5},
		{Text: claimSegment(s, th), Priority: 6},
		{Text: budgetSegment(s, th), Priority: 7},
	}

	// Drop the lowest-priority field until the row fits.
	for {
		var kept []string
		for _, seg := range segments {
			if seg.Text != "" {
				kept = append(kept, seg.Text)
			}
		}
		line := " " + strings.Join(kept, th.Colorize(th.Muted, " │ "))
		if VisibleLen(line) <= width || len(segments) <= 1 {
			return PadCell(line, width)
		}

		// Remove the current lowest priority.
		worst, idx := -1, -1
		for i, seg := range segments {
			if seg.Priority > worst {
				worst, idx = seg.Priority, i
			}
		}
		segments = append(segments[:idx], segments[idx+1:]...)
	}
}

// ProjectLabel names the project the way an operator would recognise it: the
// working directory relative to home, shortened as width demands. A synthetic
// runtime identifier is never shown, since it tells the operator nothing about
// where they are.
func ProjectLabel(s UIState, workDir string, budget int) string {
	dir := workDir
	if dir == "" {
		if cwd, err := os.Getwd(); err == nil {
			dir = cwd
		}
	}
	if dir == "" {
		// Fall back to the canonical project id only when there is no path at
		// all, and strip the runtime's generic prefix.
		name := strings.TrimPrefix(s.ProjectID, "PROJECT-")
		if name == "" || name == "local" {
			return "marshal"
		}
		return name
	}

	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, err := filepath.Rel(home, dir); err == nil && !strings.HasPrefix(rel, "..") {
			dir = "~/" + rel
		}
	}

	if VisibleLen(dir) <= budget {
		return dir
	}
	// Keep the last two path elements, which identify the project.
	parts := strings.Split(dir, string(filepath.Separator))
	if len(parts) >= 2 {
		short := ".../" + filepath.Join(parts[len(parts)-2], parts[len(parts)-1])
		if VisibleLen(short) <= budget {
			return short
		}
	}

	// A bare leaf can be meaningless on its own -- a numbered directory, say --
	// so walk back to the nearest element that actually reads as a name.
	for i := len(parts) - 1; i >= 0 && i >= len(parts)-3; i-- {
		if isMeaningfulPathSegment(parts[i]) {
			return parts[i]
		}
	}
	return filepath.Base(dir)
}

// isMeaningfulPathSegment reports whether a path element identifies a project to
// a reader. Purely numeric elements do not.
func isMeaningfulPathSegment(seg string) bool {
	if seg == "" {
		return false
	}
	for _, r := range seg {
		if r < '0' || r > '9' {
			return true
		}
	}
	return false
}

func gitSegment(s UIState, th *Theme) string {
	g := s.GitStatus
	if g.Branch == "" || g.Branch == "unknown" {
		return th.Colorize(th.Muted, "no-git")
	}
	if g.Clean {
		return fmt.Sprintf("%s %s", g.Branch, th.Colorize(th.Success, th.GlyphCheck))
	}
	return fmt.Sprintf("%s%s", g.Branch, th.Colorize(th.Warning, "*"))
}

func runtimeStateSegment(s UIState, th *Theme) string {
	state := string(s.UnderstandingState)
	if state == "" {
		state = "READY"
	}
	if s.TerminationState != "" {
		state = string(s.TerminationState)
	}
	return th.RenderBadgeText(state)
}

func agentSegment(s UIState, th *Theme) string {
	active := 0
	for _, p := range s.Participants {
		if p.IsActive {
			active++
		}
	}
	if active == 0 {
		return th.Colorize(th.Muted, "0 active")
	}
	return fmt.Sprintf("%d %s", active, th.Colorize(th.Success, th.GlyphDotFull))
}

// claimSegment surfaces contested and critical claims, which is the signal an
// operator most needs at a glance. It stays silent when there is nothing to say.
func claimSegment(s UIState, th *Theme) string {
	if len(s.Claims) == 0 {
		return ""
	}
	verified, contested, _, _ := countClaims(s.Claims)

	critical := 0
	for _, c := range s.Claims {
		if c.Criticality.IsCritical() && c.State != model.ClaimStateVerified {
			critical++
		}
	}

	out := fmt.Sprintf("C%d", len(s.Claims))
	if verified > 0 {
		out += th.Colorize(th.Success, fmt.Sprintf(" V%d", verified))
	}
	if contested > 0 {
		out += th.Colorize(th.Accent, fmt.Sprintf(" X%d", contested))
	}
	if critical > 0 {
		out += th.Colorize(th.Danger, "!")
	}
	return out
}

func budgetSegment(s UIState, th *Theme) string {
	if s.BudgetConsumed.CostUSD == nil {
		return th.Colorize(th.Muted, "$—")
	}
	return fmt.Sprintf("$%.2f", *s.BudgetConsumed.CostUSD)
}

func orDefault(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
