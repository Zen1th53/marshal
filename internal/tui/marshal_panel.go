package tui

import (
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
)

// MarshalPanel is the live view of the active Marshal run. It is a snapshot
// taken from the run after each step, so painting never reads the store.
type MarshalPanel struct {
	RunID    string
	Provider string
	State    marshal.RunState
	Tier     marshal.Tier
	Budget   marshal.Budget
	Usage    marshal.Charge
	Report   *app.MarshalCompletionReport
	Tasks    []MarshalTaskRow
	// Note is the latest thing the operator should know: what is happening,
	// or what the run is waiting for.
	Note string
}

// MarshalTaskRow is one task as the panel shows it.
type MarshalTaskRow struct {
	ID             string
	Worker         string
	Mode           marshal.WorkerMode
	ImportedResult *marshal.ImportedResult
	State          marshal.TaskState
	Returns        int
	Criteria       []string
	Files          []string
	Checks         []string
}

// newMarshalPanel snapshots a run for the panel.
func newMarshalPanel(runID, provider string, run marshal.Run, note string) *MarshalPanel {
	p := &MarshalPanel{RunID: runID, Provider: provider, State: run.State, Tier: run.Tier, Budget: run.Budget, Note: note}
	for _, t := range run.Tasks {
		returns := 0
		for _, n := range t.ReturnsByAgent {
			returns += n
		}
		row := MarshalTaskRow{ID: t.PlanTaskID, Worker: t.Worker, Mode: t.Mode, ImportedResult: t.ImportedResult, State: t.State, Returns: returns, Criteria: append([]string(nil), t.Criteria...), Files: append([]string(nil), t.Files...)}
		for _, check := range t.Checks {
			row.Checks = append(row.Checks, check.Command)
		}
		p.Tasks = append(p.Tasks, row)
	}
	return p
}

// marshalSection paints the Marshal run: its state, each task with its
// worker, state and returns, and what the run is waiting for.
func marshalSection(s UIState, th *Theme, cols int) []string {
	p := s.Marshal
	if p == nil {
		return nil
	}
	tier := string(p.Tier)
	if tier == "" {
		tier = "standard"
	}
	out := []string{PadCell(fmt.Sprintf(" %s  %s",
		th.Colorize(th.Bold, "Marshal"),
		th.Colorize(th.Muted, fmt.Sprintf("%s · %s · %s", p.RunID, tier, p.State))), cols)}
	out = append(out, PadCell("   "+marshalBudgetText(p), cols))
	for _, t := range p.Tasks {
		glyph, color := marshalTaskGlyph(th, t.State)
		line := fmt.Sprintf("   %s %-12s %-10s %s %s", th.Colorize(color, glyph), t.ID, t.Worker, t.Mode, t.State)
		if t.Returns > 0 {
			line += th.Colorize(th.Warning, fmt.Sprintf("  returned %d", t.Returns))
		}
		out = append(out, PadCell(line, cols))
	}
	if p.Note != "" {
		out = append(out, PadCell("   "+th.Colorize(th.Muted, p.Note), cols))
	}
	return out
}

func marshalTaskGlyph(th *Theme, state marshal.TaskState) (string, string) {
	switch state {
	case marshal.Merged, marshal.Accepted:
		return th.GlyphCheck, th.Success
	case marshal.Dispatched, marshal.HandedIn:
		return th.GlyphArrowR, th.Accent
	case marshal.Returned, marshal.Reassigned:
		return th.GlyphDotHalf, th.Warning
	case marshal.Escalated:
		return th.GlyphCross, th.Danger
	default:
		return th.GlyphDotEmpty, th.Muted
	}
}

func marshalBudgetText(p *MarshalPanel) string {
	amount := func(a marshal.Amount) string {
		if !a.Known {
			return "unknown"
		}
		return fmt.Sprint(a.Value)
	}
	return fmt.Sprintf("budget tokens %s (task %d / plan %d) · money %s (task %d / plan %d) · wall %ds (task %d / plan %d)", amount(p.Usage.Tokens), p.Budget.Tokens.Task, p.Budget.Tokens.Plan, amount(p.Usage.Money), p.Budget.Money.Task, p.Budget.Money.Plan, int64(p.Usage.WallTime.Seconds()), p.Budget.WallTime.Task, p.Budget.WallTime.Plan)
}

// marshalStatusText is the plain-text form of the panel for /marshal status.
func marshalStatusText(p *MarshalPanel) string {
	if p == nil {
		return "No Marshal run. Start one with /marshal <goal>."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Marshal run %s — %s\n", p.RunID, p.State)
	for _, t := range p.Tasks {
		fmt.Fprintf(&b, "  %-12s %-10s %s %s", t.ID, t.Worker, t.Mode, t.State)
		if t.Returns > 0 {
			fmt.Fprintf(&b, " (returned %d)", t.Returns)
		}
		b.WriteString("\n")
		if t.ImportedResult != nil {
			fmt.Fprintf(&b, "    imported: %s revision %d · base %s · result %s\n", t.ImportedResult.TaskID, t.ImportedResult.Revision, t.ImportedResult.BaseCommit, t.ImportedResult.ResultCommit)
		}
		if len(t.Criteria) > 0 {
			fmt.Fprintf(&b, "    criteria: %s\n", strings.Join(t.Criteria, "; "))
		}
		if len(t.Files) > 0 {
			fmt.Fprintf(&b, "    files: %s\n", strings.Join(t.Files, ", "))
		}
		if len(t.Checks) > 0 {
			fmt.Fprintf(&b, "    checks: %s\n", strings.Join(t.Checks, "; "))
		}
	}
	b.WriteString(marshalBudgetText(p) + "\n")
	if p.Note != "" {
		b.WriteString(p.Note)
	}
	if p.Report != nil {
		b.WriteString("\ncompletion report:\n")
		for _, criterion := range p.Report.Criteria {
			fmt.Fprintf(&b, "  %s / %s: %s\n", criterion.TaskID, criterion.Criterion, criterion.Status)
		}
		if len(p.Report.Untested) > 0 {
			b.WriteString("  not tested: " + strings.Join(p.Report.Untested, "; ") + "\n")
		}
		if len(p.Report.Risks) > 0 {
			b.WriteString("  risks: " + strings.Join(p.Report.Risks, "; ") + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
