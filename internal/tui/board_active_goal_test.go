package tui

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
)

type activeGoalFakeArch struct{}

func (activeGoalFakeArch) ArchitectureStatus(context.Context) (app.ArchitectureStatus, error) {
	return app.ArchitectureStatus{}, nil
}
func (activeGoalFakeArch) Open() {}

func activeGoalBoardWorkspace(t *testing.T, st *store.Store, session string) *Workspace {
	t.Helper()
	w := NewWorkspace(st, "demo", session)
	w.workDir = t.TempDir()
	w.theme = NewTheme(ThemeNoColor, true, false)
	w.screen = NewScreen(NewTerminal(strings.NewReader(""), &strings.Builder{}))
	w.homeBoard = true
	w.archSource = activeGoalFakeArch{}
	w.endOpening()
	return w
}

func activeGoalFrameText(w *Workspace, cols, rows int) string {
	lines, _, _ := w.frameLines(cols, rows)
	return StripANSI(strings.Join(lines, "\n"))
}

func activeGoalVisual(th *Theme, now time.Time) boardVisual {
	return boardVisual{
		now: now, theme: th, edition: "Marshal", empty: true, marshalLeads: true, project: "demo", focus: layout.Tasks,
		chips: []providerChip{
			{name: "codex", state: "verified", model: "gpt-5"},
			{name: "claude", state: "unverified"},
			{name: "agy", state: "limited", until: now.Add(40 * time.Minute)},
			{name: "opencode", state: "not installed"},
		},
	}
}

// TestFirstUseActiveGoalShowsWorkingState verifies that when an active goal
// exists before any run has started, the home card shows what the Marshal is
// working on (with elapsed duration) instead of asking what to build, and the
// header shows PLANNING instead of NO RUN YET.
func TestFirstUseActiveGoalShowsWorkingState(t *testing.T) {
	now := time.Date(2026, 11, 8, 14, 2, 31, 0, time.UTC)
	longGoal := "Build a local-first voice assistant with offline transcription, wake word detection, and an HTTP control panel"

	t.Run("full width unicode", func(t *testing.T) {
		th := NewTheme(ThemeNoColor, true, false)
		v := activeGoalVisual(th, now)
		v.goalGiven = true
		v.goalText = "Build a voice assistant with local transcription"
		v.goalSince = now.Add(-80 * time.Minute)
		v.hints = []string{"marshal.say", "firstuse.chat", "marshal.pick", "palette", "help"}
		var hits []boardHit
		v.hits = &hits
		l := layout.ComputeFor(120, 40, layout.Home, v.layoutCounts())
		frame := paintFirstUse(l, th, v)
		for y, line := range frame {
			if got := VisibleLen(line); got != 120 {
				t.Fatalf("row %d: width %d, want 120: %q", y, got, line)
			}
		}
		text := StripANSI(strings.Join(frame, "\n"))
		head := StripANSI(frame[1])
		if !strings.Contains(head, " PLANNING ") || strings.Contains(head, "NO RUN YET") {
			t.Fatalf("header did not switch to PLANNING: %q", head)
		}
		if strings.Contains(text, firstUseCTA) {
			t.Fatalf("home still shows first-use CTA:\n%s", text)
		}
		for _, want := range []string{
			"▸ The Marshal is working on: Build a voice assistant with local transcription",
			"1h20m",
			"The Marshal has your goal",
		} {
			if !strings.Contains(text, want) {
				t.Fatalf("home misses %q:\n%s", want, text)
			}
		}
		var chatHit bool
		for _, h := range hits {
			if h.kind == hitAction && h.id == "marshal.chat" && strings.Contains(StripANSI(frame[h.row]), "The Marshal is working on:") {
				chatHit = true
			}
		}
		if !chatHit {
			t.Fatalf("working-on-goal line is not clickable to focus chat: %+v", hits)
		}
	})

	t.Run("narrow truncates cleanly and keeps elapsed", func(t *testing.T) {
		th := NewTheme(ThemeNoColor, true, false)
		v := activeGoalVisual(th, now)
		v.goalGiven = true
		v.goalText = longGoal
		v.goalSince = now.Add(-12 * time.Minute)
		v.hints = []string{"marshal.say", "firstuse.chat", "marshal.pick", "palette", "help"}
		l := layout.ComputeFor(80, 24, layout.Home, v.layoutCounts())
		frame := paintFirstUse(l, th, v)
		for y, line := range frame {
			if got := VisibleLen(line); got != 80 {
				t.Fatalf("row %d: width %d, want 80: %q", y, got, line)
			}
		}
		text := StripANSI(strings.Join(frame, "\n"))
		if !strings.Contains(text, "▸ The Marshal is working on: Build a local-first") {
			t.Fatalf("narrow card lost goal lead:\n%s", text)
		}
		if !strings.Contains(text, "12m") {
			t.Fatalf("narrow card clipped elapsed duration:\n%s", text)
		}
		if strings.Contains(text, firstUseCTA) || strings.Contains(text, "NO RUN YET") {
			t.Fatalf("narrow card still shows idle state:\n%s", text)
		}
	})

	t.Run("ascii theme has no non-ASCII runes", func(t *testing.T) {
		th := NewTheme(ThemeNoColor, false, false)
		v := activeGoalVisual(th, now)
		v.goalGiven = true
		v.goalText = longGoal
		v.goalSince = now.Add(-45 * time.Second)
		v.hints = []string{"marshal.say", "firstuse.chat", "marshal.pick", "palette", "help"}
		l := layout.ComputeFor(80, 24, layout.Home, v.layoutCounts())
		frame := paintFirstUse(l, th, v)
		text := StripANSI(strings.Join(frame, "\n"))
		if strings.ContainsFunc(text, func(r rune) bool { return r > 127 }) {
			t.Fatalf("ASCII theme emitted non-ASCII cell:\n%s", text)
		}
		if !strings.Contains(text, "> The Marshal is working on:") || !strings.Contains(text, "45s") {
			t.Fatalf("ASCII home card misses working goal or elapsed:\n%s", text)
		}
	})
}

// TestBoardRefreshPicksUpActiveGoalFromStore verifies that when a goal contract
// is saved into goal_active after the Board opened, the architecture follower
// refreshes it off the paint path so the home and header switch from NO RUN YET
// to PLANNING and show the active goal; cancelling or delivering the goal
// returns the Board to NO RUN YET.
func TestBoardRefreshPicksUpActiveGoalFromStore(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "board-goal.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate store: %v", err)
	}

	w := activeGoalBoardWorkspace(t, st, "session-active-goal")
	defer w.Close()

	// Board v2: READY with no goal, PLANNING with the goal named on the
	// Plan pane while one is active.
	before := activeGoalFrameText(w, 120, 40)
	if !strings.Contains(lines0(before), " READY ") || !strings.Contains(before, "say what to build") {
		t.Fatalf("before goal exists:\n%s", before)
	}

	goal := model.GoalContract{
		ID:                 "GOAL-live-1",
		SessionID:          w.sessionID,
		ProjectID:          "demo",
		Revision:           1,
		OriginalRequest:    "Build a terminal RSS reader",
		DesiredOutcome:     "Build a terminal RSS reader with keyboard navigation",
		Confirmation:       model.ConfirmationApproved,
		Risk:               model.R1,
		AuthoritySource:    "owner",
		UnderstandingState: model.GoalReady,
		CreatedAt:          time.Now().UTC().Add(-25 * time.Minute),
	}
	if err := st.SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatalf("SaveGoalContract: %v", err)
	}

	w.refreshArch(ctx)
	during := activeGoalFrameText(w, 120, 40)
	head := lines0(during)
	if !strings.Contains(head, " PLANNING ") || strings.Contains(head, " READY ") {
		t.Fatalf("top bar after goal saved to store: %q", head)
	}
	if !strings.Contains(during, "PLAN · Build a terminal RSS reader with keyboard navigation") {
		t.Fatalf("the Plan pane does not name the goal:\n%s", during)
	}
	if !strings.Contains(during, "✓ goal →  reqs") {
		t.Fatalf("the Plan pane does not show the goal given and the requirements next:\n%s", during)
	}
	// The key bar also switches from "say what to build" to "say".
	if strings.Contains(during, "say what to build") || !strings.Contains(during, "s  say ") {
		t.Fatalf("the key bar still asks what to build while a goal is active:\n%s", during)
	}

	// A goal already delivered by a closed run returns to NO RUN YET.
	done := goal.OriginalRequest
	w.closedGoal.Store(&done)
	delivered := activeGoalFrameText(w, 120, 40)
	if !strings.Contains(lines0(delivered), " READY ") || !strings.Contains(delivered, "say what to build") {
		t.Fatalf("delivered goal did not return to idle home:\n%s", delivered)
	}
	w.closedGoal.Store(nil)

	// Cancelling the goal in the store returns to NO RUN YET on the next refresh.
	if _, err := st.CancelGoalContract(ctx, w.sessionID, goal.ID, goal.Revision, "owner cancelled"); err != nil {
		t.Fatalf("CancelGoalContract: %v", err)
	}
	w.refreshArch(ctx)
	cancelled := activeGoalFrameText(w, 120, 40)
	if !strings.Contains(lines0(cancelled), " READY ") || !strings.Contains(cancelled, "say what to build") || strings.Contains(cancelled, " PLANNING ") {
		t.Fatalf("cancelled goal did not return to NO RUN YET:\n%s", cancelled)
	}
}

// TestActiveGoalWithOwnerGateShowsNeedsYou verifies that when a goal is active
// AND an owner gate (e.g. architecture review) is waiting, NEEDS YOU takes
// precedence over PLANNING in the header while the body still shows the goal
// being worked on rather than the first-use call to action.
func TestActiveGoalWithOwnerGateShowsNeedsYou(t *testing.T) {
	// This test is about the classic Board itself.
	t.Setenv("MARSHAL_BOARD", "classic")
	w := activeGoalBoardWorkspace(t, nil, "session-gate")
	defer w.Close()
	w.mu.Lock()
	w.state.Goal = model.GoalContract{
		ID:             "GOAL-gate",
		Revision:       1,
		DesiredOutcome: "Build hello-uz CLI",
		Confirmation:   model.ConfirmationApproved,
		CreatedAt:      time.Now().Add(-5 * time.Minute),
	}
	w.mu.Unlock()
	w.gateContractSource = func(context.Context) (*gateContract, error) {
		return &gateContract{ID: "hello-uz", Revision: 1, Digest: "sha256:9089e7832c5c", Purpose: "Build hello-uz", Submitted: time.Now()}, nil
	}
	w.refreshGates(t.Context())

	text := activeGoalFrameText(w, 120, 40)
	head := strings.Split(text, "\n")[1]
	if !strings.Contains(head, "NEEDS YOU") || strings.Contains(head, "NO RUN YET") || strings.Contains(head, "PLANNING") {
		t.Fatalf("header with active goal and pending gate: %q", head)
	}
	if strings.Contains(text, firstUseCTA) {
		t.Fatalf("body still shows first-use CTA:\n%s", text)
	}
	if !strings.Contains(text, "▸ The Marshal is working on: Build hello-uz CLI") {
		t.Fatalf("body misses working-on-goal line:\n%s", text)
	}
}

func TestGoalElapsedFormatting(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{-5 * time.Second, "0s"},
		{0, "0s"},
		{45 * time.Second, "45s"},
		{time.Minute, "1m"},
		{12 * time.Minute, "12m"},
		{80 * time.Minute, "1h20m"},
		{2*time.Hour + 5*time.Minute, "2h05m"},
	}
	for _, tc := range cases {
		if got := goalElapsed(tc.d); got != tc.want {
			t.Errorf("goalElapsed(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

// TestActiveGoalFallbacksAndHints verifies fallback summary behavior (OriginalRequest
// when DesiredOutcome is blank, generic fallback when both are blank without ever
// showing goal ID) and that footer hints switch from say.build to marshal.say.
func TestActiveGoalFallbacksAndHints(t *testing.T) {
	w := activeGoalBoardWorkspace(t, nil, "session-fallback")
	defer w.Close()

	// OriginalRequest fallback when DesiredOutcome is empty.
	w.mu.Lock()
	w.state.Goal = model.GoalContract{
		ID:              "GOAL-secret-id",
		Revision:        1,
		OriginalRequest: "Add dark mode support\nwith user toggle",
		Confirmation:    model.ConfirmationPending,
	}
	w.mu.Unlock()
	text := activeGoalFrameText(w, 120, 40)
	if !strings.Contains(text, "PLAN · Add dark mode support") || strings.Contains(text, "with user toggle") {
		t.Fatalf("did not fall back to first line of OriginalRequest:\n%s", text)
	}
	if strings.Contains(text, "GOAL-secret-id") || strings.Contains(lines0(text), " READY ") {
		t.Fatalf("leaked goal ID or kept NO RUN YET:\n%s", text)
	}

	// Minimal goal with neither DesiredOutcome nor OriginalRequest uses fallback line.
	w.mu.Lock()
	w.state.Goal = model.GoalContract{
		ID:           "GOAL-minimal",
		Revision:     1,
		Confirmation: model.ConfirmationApproved,
	}
	w.mu.Unlock()
	text = activeGoalFrameText(w, 120, 40)
	if !strings.Contains(lines0(text), " PLANNING ") || !strings.Contains(text, "PLAN · your goal") || !strings.Contains(text, "working on your…") {
		// The NOW column keeps one cell before its rule, so at 120
		// columns the line is cut at the last whole word: "working on your…".
		t.Fatalf("minimal goal did not render fallback cleanly:\n%s", text)
	}
	if strings.Contains(text, "GOAL-minimal") || strings.Contains(text, "say what to build") {
		t.Fatalf("minimal goal leaked ID or kept CTA:\n%s", text)
	}

	// Footer hints drop say.build once an active goal exists.
	v := boardVisual{empty: true, goalGiven: true}
	ids := w.boardHints(v)
	want := []string{"marshal.say", "firstuse.chat", "marshal.pick", "palette", "help"}
	if !slices.Equal(ids, want) {
		t.Fatalf("active goal hints = %v, want %v", ids, want)
	}
}

// TestFirstUseIdleGoldenUnchanged ensures the no-goal first-use home layout
// still matches the checked-in golden byte-for-byte.
func TestFirstUseIdleGoldenUnchanged(t *testing.T) {
	now := time.Date(2026, 11, 8, 14, 2, 31, 0, time.UTC)
	for _, tc := range []struct {
		name       string
		cols, rows int
		unicode    bool
	}{
		{"first-use-home-120x40", 120, 40, true},
		{"first-use-ascii-080x24", 80, 24, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			th := NewTheme(ThemeNoColor, tc.unicode, false)
			v := activeGoalVisual(th, now)
			v.echo = []sayEcho{{id: 1, At: now.Add(-20 * time.Second), Text: "build a todo app with a REST API and tests", State: sayQueued}}
			v.hints = []string{"say.build", "firstuse.chat", "firstuse.verify", "marshal.pick", "palette", "help"}
			l := layout.ComputeFor(tc.cols, tc.rows, layout.Home, v.layoutCounts())
			frame := paintFirstUse(l, th, v)
			var b strings.Builder
			for _, line := range frame {
				b.WriteString(StripANSI(line))
				b.WriteString("|\n")
			}
			want, err := os.ReadFile(filepath.Join("testdata", "board", tc.name+".golden"))
			if err != nil {
				t.Fatalf("read golden: %v", err)
			}
			if b.String() != string(want) {
				t.Fatalf("first-use golden %s drifted:\nGOT:\n%s\nWANT:\n%s", tc.name, b.String(), string(want))
			}
		})
	}
}
