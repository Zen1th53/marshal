package tui

import (
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/model"
)

// BuildFrame composes the workspace screen from live state.
//
// The layout is deliberately unequal. Live work is the primary surface and gets
// whatever room is left; team and claims are secondary and collapse to a line
// each when they have little to say; git, budget and risk are tertiary and live
// only in the statusline. An empty section takes one line, not a bordered pane,
// so nothing static crowds out real activity.
func BuildFrame(s UIState, th *Theme, workDir string, composer *Composer, popup []string, cols, rows int) Frame {
	if th == nil {
		th = NewTheme(ThemeDefault, true, true)
	}
	if cols < 40 {
		cols = 40
	}

	f := Frame{
		Header:   buildHeader(s, th, cols),
		Body:     buildBody(s, th, cols, rows),
		Status:   RenderStatusline(s, th, workDir, cols),
		Popup:    popup,
		Composer: composer.Render(),
	}
	f.CursorCol = composer.PromptVisibleWidth() + composer.CursorPos() + 1
	return f
}

// buildHeader identifies the product and the goal once, in two lines. Mode,
// state, git and cost are not repeated here: they belong to the statusline.
func buildHeader(s UIState, th *Theme, cols int) []string {
	title := th.Colorize(th.Marshal, "MARSHAL")

	goal := s.Goal.DesiredOutcome
	if goal == "" {
		goal = th.Colorize(th.Muted, "no goal set · /goal <outcome>")
	} else {
		goal = fmt.Sprintf("%s %s",
			th.Colorize(th.Muted, fmt.Sprintf("v%d", s.Goal.Revision)),
			RedactContent(goal, s.KnownSecrets))
	}

	line := fmt.Sprintf(" %s  %s", title, goal)
	return []string{
		PadCell(line, cols),
		th.Colorize(th.Muted, strings.Repeat("─", cols)),
	}
}

// buildBody renders the live workspace: activity first, then a compact team
// line, then claims only when they exist.
func buildBody(s UIState, th *Theme, cols, rows int) []string {
	var out []string

	out = append(out, activitySection(s, th, cols)...)

	if res := outputSection(s, th, cols); len(res) > 0 {
		out = append(out, "")
		out = append(out, res...)
	}

	out = append(out, "")
	out = append(out, teamSection(s, th, cols)...)

	if claims := claimsSection(s, th, cols); len(claims) > 0 {
		out = append(out, "")
		out = append(out, claims...)
	}
	if blockers := blockerSection(s, th, cols); len(blockers) > 0 {
		out = append(out, "")
		out = append(out, blockers...)
	}
	return out
}

// activitySection is the primary surface. Each entry names its author and what
// happened, in the shape an operator reads top to bottom.
func activitySection(s UIState, th *Theme, cols int) []string {
	events := MeaningfulMessages(s.RecentMessages)

	if len(events) == 0 && s.ActiveToolCard == nil {
		return []string{
			PadCell(fmt.Sprintf(" %s  %s",
				th.Colorize(th.Bold, "Activity"),
				th.Colorize(th.Muted, "none yet")), cols),
		}
	}

	var out []string
	for _, m := range events {
		author := m.From.AgentID
		if author == "" {
			author = "marshal"
		}
		out = append(out, PadCell(fmt.Sprintf(" %s  %s",
			th.Colorize(th.Active, author),
			th.Colorize(th.Muted, strings.ToLower(string(m.Kind)))), cols))
		out = append(out, PadCell("   "+RedactContent(m.Content, s.KnownSecrets), cols))
	}

	if c := s.ActiveToolCard; c != nil {
		out = append(out, PadCell(fmt.Sprintf(" %s  %s  %s",
			th.Colorize(th.Warning, th.GlyphDotHalf),
			c.Command,
			th.Colorize(th.Muted, c.Duration.Round(1e6).String())), cols))
	}
	return out
}

// teamSection lists participants compactly. It is a list, not a table: the
// roster pane owns selection and detail.
func teamSection(s UIState, th *Theme, cols int) []string {
	if len(s.Participants) == 0 {
		return []string{
			PadCell(fmt.Sprintf(" %s  %s",
				th.Colorize(th.Bold, "Team"),
				th.Colorize(th.Muted, "no participants · /harness probe")), cols),
		}
	}

	out := []string{PadCell(" "+th.Colorize(th.Bold, "Team"), cols)}
	for _, p := range s.Participants {
		glyph := th.Colorize(th.Muted, th.GlyphDotEmpty)
		state := "IDLE"
		switch {
		case p.AgentID == s.ActiveTurn:
			glyph = th.Colorize(th.Success, th.GlyphArrowR)
			state = "WORKING"
		case !p.IsActive:
			glyph = th.Colorize(th.Danger, th.GlyphCross)
			state = "UNAVAILABLE"
		default:
			glyph = th.Colorize(th.Success, th.GlyphDotFull)
		}

		out = append(out, PadCell(fmt.Sprintf("   %s %s %s %s",
			glyph,
			PadCell(p.AgentID, 14),
			PadCell(th.Colorize(th.Muted, string(p.Role)), 12),
			th.RenderBadgeText(state)), cols))
	}
	return out
}

// claimsSection stays absent entirely when there are no claims, rather than
// reserving a pane to announce that there is nothing to show.
func claimsSection(s UIState, th *Theme, cols int) []string {
	if len(s.Claims) == 0 {
		return nil
	}

	out := []string{PadCell(fmt.Sprintf(" %s  %s",
		th.Colorize(th.Bold, "Claims"),
		th.Colorize(th.Muted, fmt.Sprintf("%d", len(s.Claims)))), cols)}

	shown := 0
	for _, c := range s.Claims {
		if shown >= 5 {
			out = append(out, PadCell(th.Colorize(th.Muted,
				fmt.Sprintf("   +%d more · /claims", len(s.Claims)-shown)), cols))
			break
		}
		crit := ""
		if c.Criticality.IsCritical() {
			crit = th.Colorize(th.Danger, " !")
		}
		out = append(out, PadCell(fmt.Sprintf("   %s %s%s  %s",
			PadCell(c.ID, 10),
			th.RenderBadgeText(string(c.State)),
			crit,
			th.Colorize(th.Muted, RedactContent(c.NormalizedText, s.KnownSecrets))), cols))
		shown++
	}
	return out
}

// outputSection shows the last command's result inside the frame. Because it is
// a section rather than direct terminal output, a long response scrolls within
// the workspace instead of pushing the statusline and composer off screen.
func outputSection(s UIState, th *Theme, cols int) []string {
	if strings.TrimSpace(s.LastOutput) == "" {
		return nil
	}

	label := th.Colorize(th.Muted, s.LastCommand)
	if s.LastOutputIsError {
		label = th.Colorize(th.Danger, s.LastCommand)
	}
	out := []string{PadCell(" "+label, cols)}

	for _, line := range strings.Split(s.LastOutput, "\n") {
		out = append(out, PadCell("   "+line, cols))
	}
	return out
}

func blockerSection(s UIState, th *Theme, cols int) []string {
	var out []string
	if s.ActiveBlocker != "" {
		out = append(out, PadCell(fmt.Sprintf(" %s  %s",
			th.Colorize(th.Danger, "Blocked"),
			RedactContent(s.ActiveBlocker, s.KnownSecrets)), cols))
	}
	if s.ActiveQuestion != "" {
		out = append(out, PadCell(fmt.Sprintf(" %s  %s",
			th.Colorize(th.Warning, "Decision"),
			RedactContent(s.ActiveQuestion, s.KnownSecrets)), cols))
	}
	return out
}

// MeaningfulMessages applies silence-by-default: routine chatter is dropped so
// the activity viewport carries findings, decisions and handoffs.
func MeaningfulMessages(msgs []model.AgentMessage) []model.AgentMessage {
	var out []model.AgentMessage
	for _, m := range msgs {
		switch m.Kind {
		case model.MessageFinding, model.MessageClaimChallenge,
			model.MessageHandoffProposal, model.MessageVerificationRequest,
			model.MessageFailedApproach:
			out = append(out, m)
		}
	}
	return out
}
