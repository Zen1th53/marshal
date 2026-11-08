package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

var menuMemoryIDs = []ActionID{"CTUI-0425", "CTUI-0429", "CTUI-0430", "CTUI-0433", "CTUI-0434", "CTUI-0436", "CTUI-0439"}

// Without the authenticated local operator authority every entry fails
// closed and says why; with it every entry is bound in the Bindings map.
func TestMenuMemoryBindingsFailClosedWithoutLocalAuthority(t *testing.T) {
	source, _ := testControl(t)
	bindings := source.Bindings()
	for _, id := range menuMemoryIDs {
		b, ok := bindings[id]
		if !ok {
			t.Fatalf("%s missing from Bindings", id)
		}
		if b.Bound() || !strings.Contains(b.Requires, "Process 07") {
			t.Fatalf("%s did not fail closed: bound=%v requires=%q", id, b.Bound(), b.Requires)
		}
		if len(b.Inputs) == 0 {
			t.Fatalf("%s has no typed form", id)
		}
		c := &Confirmation{}
		if err := c.Begin(context.Background(), b, ActionRequest{Action: id}); err == nil {
			t.Fatalf("%s opened a confirmation without authority", id)
		}
	}
}

func menuSubmit(t *testing.T, ctx context.Context, s *ControlSource, id ActionID, inputs map[string]string) (Outcome, error) {
	t.Helper()
	b := s.Bindings()[id]
	if !b.Bound() {
		t.Fatalf("%s is not bound on the runtime authority: %q", id, b.Requires)
	}
	form := newActionForm(b, ActionRequest{Action: id})
	for k, v := range inputs {
		form.values[k] = v
	}
	req, err := form.build()
	if err != nil {
		return Outcome{}, err
	}
	req.SessionID, req.ProjectID = s.SessionID, s.ProjectID
	c := &Confirmation{}
	if err := c.Begin(ctx, b, req); err != nil {
		return Outcome{}, err
	}
	c.MoveSelection(1)
	if b.Safety == SafetyDestructive {
		c.Acknowledge()
	}
	return c.Submit(ctx)
}

func TestMenuMemoryBindingsReachCanonicalServicesThroughConfirmation(t *testing.T) {
	ctx := context.Background()
	ws, runtime := realControlWorkspace(t, "SESSION-menu-memory")
	s := ws.controlSource()
	if _, err := runtime.Store().ImportTasks(ctx, []model.Task{{ID: "TASK-menu-tui", Title: "menu memory task", Status: model.TaskReady, Risk: model.R0}}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Store().RegisterAgent(ctx, model.Agent{ID: "AGENT-menu-tui", ProjectID: runtime.ProjectID(), DisplayName: "menu fixture", Role: model.RoleDeveloper, Status: model.AgentRegistered, Revision: 1}); err != nil {
		t.Fatal(err)
	}

	out, err := menuSubmit(t, ctx, s, "CTUI-0433", map[string]string{"task_id": "TASK-menu-tui", "slot_type": "decision", "value": "keep the store canonical"})
	if err != nil || !out.Succeeded() || !strings.Contains(out.Detail, "revision 1") {
		t.Fatalf("set slot: %+v %v", out, err)
	}
	if _, err := menuSubmit(t, ctx, s, "CTUI-0433", map[string]string{"task_id": "TASK-menu-tui", "slot_type": "decision", "value": "overwrite"}); err == nil || !strings.Contains(err.Error(), "Compare-and-swap") {
		t.Fatalf("set slot over an existing slot: %v", err)
	}
	if _, err := menuSubmit(t, ctx, s, "CTUI-0434", map[string]string{"task_id": "TASK-menu-tui", "slot_type": "decision", "expected_revision": "5", "value": "stale"}); err == nil {
		t.Fatal("CAS at a stale revision opened a confirmation")
	}
	out, err = menuSubmit(t, ctx, s, "CTUI-0434", map[string]string{"task_id": "TASK-menu-tui", "slot_type": "decision", "expected_revision": "1", "value": "keep the store canonical, CAS"})
	if err != nil || !out.Succeeded() || !strings.Contains(out.Detail, "revision 2") {
		t.Fatalf("cas slot: %+v %v", out, err)
	}

	out, err = menuSubmit(t, ctx, s, "CTUI-0436", map[string]string{"operation": "grant", "task_id": "TASK-menu-tui", "agent_id": "AGENT-menu-tui"})
	if err != nil || !out.Succeeded() || !strings.Contains(out.Detail, "granted") {
		t.Fatalf("grant: %+v %v", out, err)
	}
	if ok, err := runtime.Store().HasActiveRoleBinding(ctx, "AGENT-menu-tui", "TASK-menu-tui"); err != nil || !ok {
		t.Fatalf("grant not effective: %v %v", ok, err)
	}
	out, err = menuSubmit(t, ctx, s, "CTUI-0436", map[string]string{"operation": "revoke", "task_id": "TASK-menu-tui", "agent_id": "AGENT-menu-tui"})
	if err != nil || !out.Succeeded() || !strings.Contains(out.Detail, "revoked") {
		t.Fatalf("revoke: %+v %v", out, err)
	}
	if _, err := menuSubmit(t, ctx, s, "CTUI-0436", map[string]string{"operation": "grant", "task_id": "TASK-menu-tui", "agent_id": "AGENT-menu-tui"}); err == nil {
		t.Fatal("a revoked grant was re-issued")
	}

	before, err := runtime.Store().ListMemoryV2(ctx, memoryFilterFor(runtime.ProjectID()))
	if err != nil {
		t.Fatal(err)
	}
	out, err = menuSubmit(t, ctx, s, "CTUI-0439", map[string]string{"task_id": "TASK-menu-tui"})
	if err != nil || !out.Succeeded() || !strings.Contains(out.Detail, "1 slot(s)") || !strings.Contains(out.Detail, "not submitted") {
		t.Fatalf("compile handoff: %+v %v", out, err)
	}
	after, err := runtime.Store().ListMemoryV2(ctx, memoryFilterFor(runtime.ProjectID()))
	if err != nil || len(after) != len(before) {
		t.Fatalf("compiling a handoff wrote memory: %d -> %d %v", len(before), len(after), err)
	}

	// Run-bound entries name the missing run instead of inventing one.
	for _, id := range []ActionID{"CTUI-0425", "CTUI-0430"} {
		_, err := menuSubmit(t, ctx, s, id, map[string]string{"task_id": "TASK-menu-tui", "kind": "semantic", "title": "t", "body": "b"})
		if err == nil || !strings.Contains(err.Error(), "run") {
			t.Fatalf("%s without a run: %v", id, err)
		}
	}
	if _, err := menuSubmit(t, ctx, s, "CTUI-0429", map[string]string{"memory_id": "MEM-missing", "artifact_id": "ART-missing"}); err == nil {
		t.Fatal("artifact attached to a missing record")
	}
}

func memoryFilterFor(projectID string) store.MemoryQueryFilter {
	return store.MemoryQueryFilter{ProjectID: projectID}
}
