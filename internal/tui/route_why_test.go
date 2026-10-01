package tui

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/testutil/testcloud"
)

func TestRouteNamesNoModelAndValidatesPreferences(t *testing.T) {
	ws := NewWorkspace(nil, "proj", "sess")
	ws.workDir = t.TempDir()
	ctx := t.Context()

	out, _ := ws.ExecuteCommand(ctx, "/route harness=cursor")
	if !strings.Contains(out, "Route not computed") || !strings.Contains(out, "unknown harness") {
		t.Fatalf("unknown harness: %q", out)
	}
	out, _ = ws.ExecuteCommand(ctx, "/route role=architect harness=opencode")
	if !strings.Contains(out, "Preference:   preference opencode is not used for role architect") {
		t.Fatalf("incompatible preference not reported: %q", out)
	}
	for _, fabricated := range []string{"gpt-4o", "claude-3-7-sonnet", "deepseek-coder", "gemini-2.5-pro", "o1"} {
		if strings.Contains(out, "Model:        "+fabricated) {
			t.Fatalf("route named an invented model %s:\n%s", fabricated, out)
		}
	}
	if !strings.Contains(out, "Model:        provider default, resolved at dispatch") {
		t.Fatalf("route model line: %q", out)
	}
}

func TestWhyExplainsTheLastRouteForTheCurrentGoal(t *testing.T) {
	ws := NewWorkspace(nil, "proj", "sess")
	ws.workDir = t.TempDir()
	ws.AttachULTRA(testcloud.EntitledGate(t, testcloud.Options{}), false)
	ctx := t.Context()
	ws.mu.Lock()
	ws.state.Goal = model.GoalContract{ID: "GOAL-why", Revision: 1}
	ws.mu.Unlock()

	if _, err := ws.cmd.Handle(ctx, "/route role=qa risk=R3"); err != nil {
		t.Fatal(err)
	}
	out, _ := ws.cmd.Handle(ctx, "/why")
	if !strings.Contains(out, "Request: role=qa, risk=R3") || !strings.Contains(out, "selected for qa") {
		t.Fatalf("/why did not explain the last route: %q", out)
	}
	if ws.GetUIState().RouteExplanation != "" {
		t.Fatal("advisory route leaked into applied UI state")
	}

	ws.mu.Lock()
	ws.state.Goal.Revision = 2
	ws.mu.Unlock()
	out, _ = ws.cmd.Handle(ctx, "/why")
	if !strings.Contains(out, "goal has changed since") {
		t.Fatalf("stale route presented as current: %q", out)
	}
}
