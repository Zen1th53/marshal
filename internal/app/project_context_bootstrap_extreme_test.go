//go:build !governed

package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
)

func TestRequiredMarshalFirstGoalPlanningDoesNotRequireInventedPlan(t *testing.T) {
	r, b, record, _ := requiredContextFixture(t, true)
	control, err := r.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx := control.Context(t.Context())
	m := &MarshalCLI{Provider: "codex", runtime: r, ProjectID: r.ProjectIdentity(), Dir: r.ProjectRoot(), requiredGoalSession: b.Session}
	_, text, finish, err := m.prepareRequiredMarshalModelTurn(ctx, "Plan the first real canonical goal")
	if err != nil {
		t.Fatal("first goal planning was blocked by missing plan", err)
	}
	if !strings.Contains(text, record.Body) || !strings.Contains(text, "Preserve reliable project execution") {
		t.Fatal("first plan omitted accepted task-only memory or purpose")
	}
	if err := finish(); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "host-cli")
	script := "#!/bin/sh\nprintf '%s\\n' '{\"type\":\"thread.started\",\"thread_id\":\"host-bootstrap\"}' '{\"type\":\"item.completed\",\"item\":{\"type\":\"agent_message\",\"text\":\"{\\\"ok\\\":true}\"}}'\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	m.Binary = binary
	var out struct {
		OK bool `json:"ok"`
	}
	if err := m.turn(ctx, "Plan the first real canonical goal", `{"type":"object","properties":{"ok":{"type":"boolean"}}}`, &out); err != nil || !out.OK {
		t.Fatal("actual first planning invocation refused", err)
	}
	proof, err := m.takeRequiredMarshalTurnCapture(r)
	if err != nil || proof.preparation.receipt.Binding.Session != b.Session || proof.preparation.receipt.Binding.GoalID != b.GoalID || proof.preparation.receipt.Binding.PlanID != "" || proof.preparation.receipt.Binding.RunID != "" {
		t.Fatal("planning invocation invented execution identity", err)
	}
	if _, err := r.store.GetActivePlan(ctx, projectid.ID(r.ProjectIdentity())); !errors.Is(err, plan.ErrPlanNotFound) {
		t.Fatal("first planning invocation created a canonical plan", err)
	}
	for _, session := range []string{"", "foreign-goal-session"} {
		m.requiredGoalSession = session
		if _, _, _, err := m.prepareRequiredMarshalModelTurn(ctx, "Plan"); err == nil {
			t.Fatal("missing/foreign canonical goal chosen implicitly")
		}
	}
}
