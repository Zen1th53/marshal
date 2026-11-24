package tui

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/settingscatalog"
)

// Completion offers exactly the /marshal words the command reference lists,
// plus the guidance words, and every dispatched word is offered.
func TestMarshalCompletionMatchesRegisteredCommands(t *testing.T) {
	w := NewWorkspace(nil, "project", "session")
	defer w.Close()
	got := w.completer.ctx.Subcommands["/marshal"]
	for _, name := range []string{"retry", "reassign", "cancel", "draft", "import", "chat", "architecture", "web", "approve", "status", "close", "amend", "resume", "stop", "model", "settings", "help", "accept", "return", "use-plan", "approve-task", "pause", "models", "deliver", "plan-show", "plan-approve", "plan-amend"} {
		if !slices.Contains(got, name) {
			t.Errorf("completion misses /marshal %s", name)
		}
	}
	for _, name := range got {
		if !strings.Contains(marshalUsage, "/marshal "+name) && marshalGuidance(name) == "" && name != "help" {
			t.Errorf("completion offers /marshal %s, which neither the reference nor guidance describes", name)
		}
	}
	for _, name := range allMarshalSubcommands {
		if !slices.Contains(got, name) {
			t.Errorf("typo list names /marshal %s but completion does not", name)
		}
	}
	for path, want := range map[string]string{"/view": "classic", "/marshal draft": "discard", "/goal cancel": "confirm", "/marshal settings": "failover-order"} {
		if !slices.Contains(w.completer.ctx.Subcommands[path], want) {
			t.Errorf("completion of %q misses %q", path, want)
		}
	}
	if !slices.Contains(w.completer.ctx.Commands, "/blind") {
		t.Error("completion misses /blind")
	}
	help := (&CommandHandler{}).helpText()
	for _, want := range []string{"/view [board|classic", "/marshal retry", "/marshal reassign", "/marshal cancel", "/marshal draft discard", "/marshal import", "Guidance only"} {
		if !strings.Contains(help, want) {
			t.Errorf("help misses %q", want)
		}
	}
}

// Guidance words name real commands and run nothing.
func TestMarshalGuidanceRunsNothing(t *testing.T) {
	h := &CommandHandler{}
	for _, sub := range []string{"pause", "models", "deliver", "plan-show", "plan-approve"} {
		out, err := h.handleMarshal(nil, []string{sub})
		if err != nil || !strings.Contains(out, "Nothing was run") {
			t.Errorf("/marshal %s: %q, %v", sub, out, err)
		}
	}
}

// Help lists exactly the mouse targets the Board draws, and none of the
// task evidence keys and clicks that are not available.
func TestMouseHelpMatchesHitRegions(t *testing.T) {
	v := boardVisual{theme: NewTheme(ThemeNoColor, true, true)}
	help, _, _ := v.helpOverlay(160, 80, 0)
	got := StripANSI(strings.Join(help, "\n"))
	for _, m := range boardMouseHelp {
		if !strings.Contains(got, m.Where) || !strings.Contains(got, m.What) {
			t.Errorf("help misses mouse line %q", m.Where)
		}
	}
	kinds := map[string]boardHitKind{"header chip": hitChip, "footer key": hitAction}
	if len(boardMouseHelp) != len(kinds) {
		t.Fatalf("mouse help lists %d targets, drawn regions cover %d", len(boardMouseHelp), len(kinds))
	}
	for _, m := range boardMouseHelp {
		if kinds[m.Where] == "" {
			t.Errorf("mouse help line %q has no drawn hit region", m.Where)
		}
	}
	if !boardChipClickable("inbox") {
		t.Error("help lists header chips but none is clickable")
	}
	_, _, hits := v.helpOverlay(160, 80, 0)
	if len(hits) == 0 {
		t.Error("help lists footer keys but draws no click region for them")
	}
	for _, h := range hits {
		if h.kind != hitAction || h.id == "" {
			t.Errorf("help draws a %q region %q that its mouse lines do not describe", h.kind, h.id)
		}
	}
	for _, dead := range []string{"inspect the selected task", "against base", "d  D", "inspector"} {
		if strings.Contains(got, dead) {
			t.Errorf("help advertises %q", dead)
		}
	}
	for _, k := range boardKeymap {
		if k.ID == "plan.inspect" {
			t.Error("the plan inspector key is still listed")
		}
	}
}

// A failover order and every catalogue key parse through one path, however
// the order is spaced.
func TestSettingsArgumentsParseConsistently(t *testing.T) {
	for _, in := range [][]string{
		{"failover-order", "claude,codex"},
		{"failover-order", "claude", "codex"},
		{"failover-order", "claude", "->", "codex"},
		{"failover-order", "claude -> codex"},
		{"failover-order", "claude,", "codex"},
	} {
		got := joinFailoverArgs(in)
		if len(got) != 2 {
			t.Errorf("%v folded to %v", in, got)
		}
	}
	w := NewWorkspace(nil, "project", "session")
	defer w.Close()
	for _, d := range settingscatalog.Catalogue() {
		if !slices.Contains(w.completer.ctx.Subcommands["/marshal settings"], d.Key) {
			t.Errorf("completion misses setting %s", d.Key)
		}
		if _, ok := w.completer.ctx.Descriptions["/marshal settings "+d.Key]; !ok {
			t.Errorf("no help for setting %s", d.Key)
		}
	}
}

func TestFailoverOrderForms(t *testing.T) {
	if _, err := settingscatalog.Apply(marshal.DefaultSettings(), "failover-order", "claude,codex"); err != nil && strings.Contains(err.Error(), "not configurable") {
		t.Skip("failover is not in this edition")
	}
	for _, v := range []string{"claude,codex", "claude, codex", "claude codex", "claude -> codex", "claude->codex"} {
		if _, err := settingscatalog.Apply(marshal.DefaultSettings(), "failover-order", v); err != nil {
			t.Errorf("%q: %v", v, err)
		}
	}
}

// A goal that merely starts with a guidance word is still a goal.
func TestMarshalGuidanceWordsStartingAGoalAreNotGuidance(t *testing.T) {
	for _, sub := range []string{"deliver", "pause", "models", "plan-show"} {
		if marshalGuidance(sub) == "" {
			t.Fatalf("%s is not guidance", sub)
		}
	}
	w := NewWorkspace(nil, "project", "session")
	defer w.Close()
	h := &CommandHandler{ws: w}
	out, _ := h.handleMarshal(context.Background(), []string{"deliver", "the", "onboarding", "flow"})
	if strings.Contains(out, "Nothing was run") {
		t.Errorf("a goal was answered as guidance: %q", out)
	}
}

// Every unlocked catalogue key applies a value of its own shape.
func TestEveryUnlockedSettingApplies(t *testing.T) {
	for _, d := range settingscatalog.Catalogue() {
		if d.LockReason != "" {
			continue
		}
		v := "1"
		if len(d.Values) > 0 {
			v = d.Values[0]
		}
		if d.Key == "failover-order" {
			v = "claude,codex"
		}
		if _, err := settingscatalog.Apply(marshal.DefaultSettings(), d.Key, v); err != nil && !strings.Contains(err.Error(), "not configurable") {
			t.Errorf("%s %s: %v", d.Key, v, err)
		}
	}
}
