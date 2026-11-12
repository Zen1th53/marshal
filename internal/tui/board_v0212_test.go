package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
	"github.com/Zen1th53/marshal/internal/usage"
)

// TestMarshalPickerWordsMatchLaunchPicker: the m picker says "limit unknown"
// where the launch picker does, never "unavailable", and every row says
// whether a lane is running on it: "busy" or "free".
func TestMarshalPickerWordsMatchLaunchPicker(t *testing.T) {
	th := NewTheme(ThemeNoColor, true, true)
	v := boardVisual{theme: th, now: time.Now()}
	cands := []pickerCand{{provider: "codex", known: true, left: 29}, {provider: "claude"}, {provider: "agy", busy: true}}
	got := StripANSI(strings.Join(v.marshalPickerOverlay(layout.Rect{W: 80, H: 8}, marshalPickerView{open: true, cands: cands, current: "codex"}), "\n"))
	if strings.Contains(got, "unavailable") {
		t.Errorf("the m picker says unavailable:\n%s", got)
	}
	for _, want := range []string{"Codex         now  29% left   free", "Claude Code        limit unknown   free", "Antigravity        limit unknown   busy"} {
		if !strings.Contains(got, want) {
			t.Errorf("the m picker lacks %q:\n%s", want, got)
		}
	}
	launch := StripANSI(boardVisual{theme: th, now: time.Now(), pick: pickerView{cands: cands}}.pickerBody(160, 6, 0)[0])
	if !strings.Contains(launch, "claude "+limitUnknown) {
		t.Errorf("the launch picker's word changed: %q", launch)
	}
}

// TestBoardPlanningAfterAccept: from the browser Accept until the run exists
// the empty Board says the Marshal is planning, not NO RUN YET; once a run
// is on screen the state ends.
func TestBoardPlanningAfterAccept(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	w.endOpening()
	frame := func() string {
		lines, _, _ := w.frameLines(160, 40)
		return StripANSI(strings.Join(lines, "\n"))
	}
	if got := frame(); !strings.Contains(got, "NO RUN YET") || strings.Contains(got, planningCard) {
		t.Fatalf("before the Accept:\n%s", got)
	}
	w.planning.Store(true) // what browserAccepted sets
	got := frame()
	if strings.Contains(got, "NO RUN YET") || !strings.Contains(got, " PLANNING ") || !strings.Contains(got, planningCard) {
		t.Fatalf("after the Accept:\n%s", got)
	}
	if strings.Contains(got, firstUseCTA) {
		t.Errorf("the planning Board still asks what to build:\n%s", got)
	}
	if w.planningNow(false, model.GoalContract{}) || w.planning.Load() {
		t.Fatal("the planning state outlived the run's start")
	}
	if got := frame(); !strings.Contains(got, "NO RUN YET") {
		t.Fatalf("after the run started and closed:\n%s", got)
	}
}

// TestProvidersViewListsInstalledProviders: with no harness profile, each
// installed provider is a row with its real limit (the vitals reader) and a
// plain state, and an unreadable limit reads "limit unknown".
func TestProvidersViewListsInstalledProviders(t *testing.T) {
	now := memEpoch
	th := NewTheme(ThemeNoColor, true, true)
	v := boardVisual{theme: th, now: now}
	rows := installedRows(nil, map[string]bool{"codex": true, "claude": true, "agy": false, "opencode": true})
	if len(rows) != 3 || rows[0].Provider != "codex" || rows[1].Provider != "claude" || rows[2].Provider != "opencode" {
		t.Fatalf("installed rows: %+v", rows)
	}
	if again := installedRows([]store.ProviderStatus{{Provider: "claude-code"}}, map[string]bool{"claude": true}); len(again) != 0 {
		t.Fatalf("a provider the overview lists got a second row: %+v", again)
	}
	m := providerPaint{Snap: providerSnapshot{Loaded: true, At: now, Installed: rows}, Quotas: map[string]usage.Quota{"codex": quotaLeft("codex", 29, now.Add(26*time.Hour))}, Selected: "claude"}
	lines, _ := v.providerLines(m, 160)
	text := strings.Join(lines, "\n")
	for _, want := range []string{"codex", "29% left", "claude", limitUnknown, "opencode", "unverified", "claude is installed but not verified yet; [v] verifies it"} {
		if !strings.Contains(text, want) {
			t.Errorf("Providers view lacks %q:\n%s", want, text)
		}
	}
	if strings.Count(text, "unknown") != strings.Count(text, limitUnknown) {
		t.Errorf("a limit reads other than %q:\n%s", limitUnknown, text)
	}
	for _, absent := range []string{"no harness profiles", "/harness verify", "re-verify"} {
		if strings.Contains(text, absent) {
			t.Errorf("Providers view shows %q:\n%s", absent, text)
		}
	}
}

// TestBoardPlanningFromStoredAccept: a fresh Workspace (a restart mid
// planning) over a stored accepted architecture, a goal on record and no run
// shows PLANNING; a cancelled goal, or a goal a closed run already delivered,
// shows NO RUN YET.
func TestBoardPlanningFromStoredAccept(t *testing.T) {
	frame := func(w *Workspace) string {
		lines, _, _ := w.frameLines(160, 40)
		return StripANSI(strings.Join(lines, "\n"))
	}
	fresh := func(t *testing.T, confirmation model.ConfirmationState) *Workspace {
		w := emptyBoardWorkspace(t)
		w.endOpening()
		w.archSource = &fakeArchView{status: app.ArchitectureStatus{Exists: true, Revision: 2, Accepted: true}}
		w.state.Goal = model.GoalContract{ID: "GOAL-1", OriginalRequest: "add square(n)", Confirmation: confirmation}
		w.refreshArch(t.Context()) // the follower's first read at start
		return w
	}
	w := fresh(t, model.ConfirmationApproved)
	defer w.Close()
	if w.planning.Load() {
		t.Fatal("the test must not lean on the in-memory Accept flag")
	}
	if got := frame(w); !strings.Contains(got, " PLANNING ") || !strings.Contains(got, planningCard) || strings.Contains(got, "NO RUN YET") {
		t.Fatalf("restart over a stored Accept:\n%s", got)
	}
	done := "add square(n)"
	w.closedGoal.Store(&done)
	if got := frame(w); !strings.Contains(got, "NO RUN YET") {
		t.Fatalf("a goal a closed run delivered still plans:\n%s", got)
	}
	c := fresh(t, model.ConfirmationCancelled)
	defer c.Close()
	if got := frame(c); !strings.Contains(got, "NO RUN YET") || strings.Contains(got, " PLANNING ") {
		t.Fatalf("a cancelled goal still plans:\n%s", got)
	}
}
