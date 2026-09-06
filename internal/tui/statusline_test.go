package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

// TestProjectLabelNamesSomethingRecognisable proves the statusline identifies
// the project by a path the operator recognises, never by the runtime's
// synthetic "PROJECT-local" identifier, which says nothing about where they are.
func TestProjectLabelNamesSomethingRecognisable(t *testing.T) {
	state := UIState{ProjectID: "PROJECT-local"}

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory on this host")
	}

	dir := filepath.Join(home, "Desktop", "codex", "marshal")
	got := ProjectLabel(state, dir, 40)
	if got != "~/Desktop/codex/marshal" {
		t.Errorf("ProjectLabel = %q, want the home-relative path", got)
	}

	// Narrow budgets shorten rather than degrade to a placeholder.
	if short := ProjectLabel(state, dir, 18); !strings.Contains(short, "marshal") {
		t.Errorf("shortened label %q lost the project name", short)
	}
	if shortest := ProjectLabel(state, dir, 8); shortest != "marshal" {
		t.Errorf("shortest label = %q, want %q", shortest, "marshal")
	}

	// Never surface the synthetic identifier.
	for _, budget := range []int{8, 18, 40} {
		if strings.Contains(ProjectLabel(state, dir, budget), "PROJECT-local") {
			t.Errorf("label at budget %d leaked the runtime placeholder", budget)
		}
	}
}

// TestProjectLabelSkipsMeaninglessLeaf proves a numeric leaf directory, which
// identifies nothing to a reader, is passed over for the nearest element that
// actually names something.
func TestProjectLabelSkipsMeaninglessLeaf(t *testing.T) {
	state := UIState{ProjectID: "PROJECT-local"}

	got := ProjectLabel(state, "/tmp/TestSomething1234/001", 10)
	if got == "001" {
		t.Errorf("label kept a meaningless numeric leaf: %q", got)
	}
	if !strings.Contains(got, "Test") {
		t.Errorf("label %q does not identify the project", got)
	}
}

// TestStatuslineIsExactlyOneRow proves the statusline always occupies exactly
// the requested width on a single line. A statusline that wraps would push the
// composer off screen.
func TestStatuslineIsExactlyOneRow(t *testing.T) {
	th := NewTheme(ThemeDefault, true, true)
	cost := 1.25
	state := UIState{
		ProjectID:          "PROJECT-local",
		SessionMode:        "ULTRA",
		UnderstandingState: model.GoalReady,
		GitStatus:          GitStatusResult{Branch: "feat/a-very-long-branch-name", Clean: false, ChangedCount: 7},
		Participants: []model.Participant{
			{AgentID: "claude", IsActive: true},
			{AgentID: "codex", IsActive: true},
		},
		Claims: []model.Claim{
			{ID: "C-1", State: model.ClaimStateContested},
			{ID: "C-2", State: model.ClaimStateVerified},
		},
		BudgetConsumed: model.ConsumedBudget{CostUSD: &cost},
	}

	for _, width := range []int{40, 60, 80, 100, 120, 200} {
		line := RenderStatusline(state, th, "/home/u/proj", width)
		if strings.Contains(line, "\n") {
			t.Errorf("width %d: statusline contains a newline", width)
		}
		if got := VisibleLen(line); got != width {
			t.Errorf("width %d: statusline visible width is %d", width, got)
		}
	}
}

// TestStatuslineDropsByPriority proves a narrow terminal keeps identity and git
// state and sheds the lower-priority fields, rather than truncating everything
// into uselessness.
func TestStatuslineDropsByPriority(t *testing.T) {
	th := NewTheme(ThemeNoColor, false, false)
	cost := 9.99
	state := UIState{
		ProjectID:          "PROJECT-local",
		SessionMode:        "ULTRA",
		UnderstandingState: model.GoalReady,
		GitStatus:          GitStatusResult{Branch: "main", Clean: true},
		Participants:       []model.Participant{{AgentID: "codex", IsActive: true}},
		BudgetConsumed:     model.ConsumedBudget{CostUSD: &cost},
	}

	wide := StripANSI(RenderStatusline(state, th, "/home/u/proj", 120))
	for _, want := range []string{"proj", "main", "ULTRA", "READY"} {
		if !strings.Contains(wide, want) {
			t.Errorf("wide statusline missing %q: %s", want, wide)
		}
	}

	narrow := StripANSI(RenderStatusline(state, th, "/home/u/proj", 34))
	if !strings.Contains(narrow, "proj") {
		t.Errorf("narrow statusline dropped the project identity: %q", narrow)
	}
	if strings.Contains(narrow, "9.99") {
		t.Errorf("narrow statusline kept the lowest-priority budget field: %q", narrow)
	}
}

// TestComposerPromptIsSingleLine is the regression guard for the reported bug.
// The prompt previously embedded a newline and a full status banner, so every
// keystroke printed another banner into scrollback.
func TestComposerPromptIsSingleLine(t *testing.T) {
	th := NewTheme(ThemeDefault, true, true)
	c := NewComposer(th)
	c.SetPrompt(ComposerPromptInfo{Project: "PROJECT-local", Mode: "ULTRA", State: "READY"})

	prompt := c.PromptString()
	if strings.Contains(prompt, "\n") {
		t.Fatalf("composer prompt contains a newline: %q", prompt)
	}
	plain := StripANSI(prompt)
	for _, banned := range []string{"MARSHAL]", "PROJECT-local", "ULTRA", "READY"} {
		if strings.Contains(plain, banned) {
			t.Errorf("composer prompt repeats statusline context %q: %q", banned, plain)
		}
	}

	c.SetText("/mode")
	rendered := c.Render()
	if strings.Contains(rendered, "\n") {
		t.Errorf("composer render spans multiple lines: %q", rendered)
	}
}
