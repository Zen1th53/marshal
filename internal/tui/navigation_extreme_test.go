//go:build !governed

package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
)

func TestExtremeMenuReleaseAndManifestBindings(t *testing.T) {
	ia, err := FrozenIA()
	if err != nil || len(ia.Sections) != 9 || len(ia.Gaps()) != 9 {
		t.Fatalf("Marshal IA sections/gaps: %v %v", ia, err)
	}
	for id := range buildTaskControlOperations {
		node, found := ia.Node(string(id))
		if !found || node.Binding != BindingBound || !strings.Contains(strings.Join(node.BackendSources, " "), "task_commands.go") {
			t.Fatalf("implemented task control %s is still declared unavailable", id)
		}
	}
	for _, id := range []string{"CTUI-0148"} {
		node, _ := ia.Node(id)
		if node.Binding != BindingGap {
			t.Fatalf("unimplemented %s fabricated a binding", id)
		}
	}
}

func TestExtremeMenuShowsActualMissingActionBindingBeforeActivation(t *testing.T) {
	view, _ := testControlView(t)
	ia, err := FrozenIA()
	if err != nil {
		t.Fatal(err)
	}
	missing, _ := ia.Node("CTUI-0203")
	if !Availability(missing).Enabled {
		t.Fatal("fixture no longer represents a statically declared action")
	}
	availability := view.buildActionAvailability(missing)
	if availability.Enabled || !strings.Contains(availability.Reason, "marshal init") {
		t.Fatalf("unattached mutation advertised as available: %+v", availability)
	}
	if rendered := strings.Join(view.renderActionBar(missing, 120), "\n"); !strings.Contains(rendered, "UNAVAILABLE:") || strings.Contains(rendered, "This action is available") {
		t.Fatalf("missing typed/runtime boundary was hidden: %q", rendered)
	}
	implemented, _ := ia.Node("CTUI-0043")
	originalAuthority := view.control.source.Authority.(*fakeAuthority)
	view.control.source.Authority = struct{ ControlAuthority }{originalAuthority}
	for _, id := range []string{"CTUI-0077", "CTUI-0078", "CTUI-0079", "CTUI-0080", "CTUI-0081", "CTUI-0082"} {
		budget, _ := ia.Node(id)
		if availability := view.buildActionAvailability(budget); availability.Enabled || !strings.Contains(availability.Reason, "contract") {
			t.Fatal("contract-edit refusal callback was presented as an operational budget setter")
		}
	}
	if availability := view.buildActionAvailability(implemented); availability.Enabled || !strings.Contains(availability.Reason, "task control") {
		t.Fatal("authority without task control was advertised as operational")
	}
	view.control.source.Authority = &menuTaskAuthority{fakeAuthority: originalAuthority}
	if availability := view.buildActionAvailability(implemented); !availability.Enabled {
		t.Fatal("implemented task action was disabled", availability.Reason)
	}
	view.control.source = nil
	if availability := view.buildActionAvailability(implemented); availability.Enabled || !strings.Contains(availability.Reason, "authority") {
		t.Fatal("no authority was silently treated as operational")
	}
}

func TestExtremeMenuTaskControlsCanonicalReadbackAndReplay(t *testing.T) {
	ws, runtime := realControlWorkspace(t, "SESSION-menu-tasks")
	ws.AttachRuntime(runtime, projectid.ID(runtime.ProjectIdentity()))
	ctx := t.Context()
	if _, err := runtime.ImportTasks(ctx, []model.Task{{ID: "TASK-menu-ready", Title: "menu ready", Status: model.TaskReady, Risk: model.R1},
		{ID: "TASK-menu-blocked", Title: "menu blocked", Status: model.TaskBlocked, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	source := ws.controlSource()
	for _, step := range []struct {
		id          ActionID
		task, state string
		attempt     int
	}{
		{"CTUI-0043", "TASK-menu-ready", "paused", 1},
		{"CTUI-0044", "TASK-menu-ready", "", 1},
		{"CTUI-0045", "TASK-menu-blocked", "", 2},
	} {
		binding := source.Bindings()[step.id]
		confirmation := &Confirmation{}
		request := ActionRequest{Action: step.id, ProjectID: source.ProjectID, SessionID: source.SessionID, Inputs: map[string]string{"task_id": step.task}}
		if err := confirmation.Begin(ctx, binding, request); err != nil {
			t.Fatal(err)
		}
		confirmation.MoveSelection(1)
		outcome, err := confirmation.Submit(ctx)
		if err != nil || !outcome.Succeeded() {
			t.Fatalf("%s canonical outcome: %+v %v", step.id, outcome, err)
		}
		proof, err := runtime.Task(ctx, step.task)
		if err != nil || proof.ControlState != step.state || proof.Attempt != step.attempt {
			t.Fatalf("%s durable proof: %+v %v", step.id, proof, err)
		}
		// Replay the exact envelope through the backend, not only the view's
		// duplicate-enter guard: one idempotency key must not change revision.
		replayed, err := binding.Execute(ctx, confirmation.Request())
		if err != nil || !replayed.Succeeded() {
			t.Fatalf("%s exact retry: %+v %v", step.id, replayed, err)
		}
		after, _ := runtime.Task(ctx, step.task)
		if after.Revision != proof.Revision || after.Attempt != proof.Attempt {
			t.Fatal("duplicate task control mutated the canonical result")
		}
	}
}

func TestExtremeMenuCancelExactTaskDestructiveAndPersistent(t *testing.T) {
	ws, runtime := realControlWorkspace(t, "SESSION-menu-cancel")
	ws.AttachRuntime(runtime, projectid.ID(runtime.ProjectIdentity()))
	ctx := t.Context()
	if _, err := runtime.ImportTasks(ctx, []model.Task{
		{ID: "TASK-a-sibling", Title: "untouched sibling", Status: model.TaskReady, Risk: model.R1},
		{ID: "TASK-z-cancel", Title: "chosen cancellation", Status: model.TaskReady, Risk: model.R1},
	}); err != nil {
		t.Fatal(err)
	}
	source := ws.controlSource()
	beforePause, err := runtime.Task(ctx, "TASK-z-cancel")
	if err != nil {
		t.Fatal(err)
	}
	paused, err := source.Authority.(taskControlAuthority).CommandTask(ctx, app.CommandEnvelope{ProjectID: source.ProjectID,
		SessionID: source.SessionID, TargetID: "TASK-z-cancel", ExpectedVersion: beforePause.Revision, IdempotencyKey: "prepare-cancel-paused"}, app.TaskCommand{Operation: "pause"})
	if err != nil || paused.ControlState != "paused" {
		t.Fatal(paused, err)
	}
	sibling, _ := runtime.Task(ctx, "TASK-a-sibling")
	binding := source.Bindings()["CTUI-0046"]
	if binding.Safety != SafetyDestructive || len(binding.Inputs) != 1 || !binding.Inputs[0].Required {
		t.Fatal("cancellation lost destructive confirmation or exact task input")
	}
	confirmation := &Confirmation{}
	if err := confirmation.Begin(ctx, binding, ActionRequest{Action: binding.Action, ProjectID: source.ProjectID,
		SessionID: source.SessionID, Inputs: map[string]string{"task_id": "TASK-z-cancel"}}); err != nil {
		t.Fatal(err)
	}
	if confirmation.Request().Target.ID != "TASK-z-cancel" || confirmation.Request().Target.Revision != paused.Revision {
		t.Fatal("confirmation chose a sibling or stale revision")
	}
	confirmation.MoveSelection(1)
	if _, err := confirmation.Submit(ctx); err == nil {
		t.Fatal("unacknowledged destructive cancellation submitted")
	}
	unchanged, _ := runtime.Task(ctx, "TASK-z-cancel")
	if unchanged.Revision != paused.Revision {
		t.Fatal("confirmation alone mutated the task")
	}
	confirmation.Acknowledge()
	outcome, err := confirmation.Submit(ctx)
	if err != nil || !outcome.Succeeded() || !strings.Contains(outcome.Detail, "worker exit is not confirmed") {
		t.Fatal(outcome, err)
	}
	proof, err := runtime.Task(ctx, "TASK-z-cancel")
	if err != nil || proof.Status != model.TaskCancelled || proof.ControlState != "cancelled" || proof.Revision != paused.Revision+1 {
		t.Fatal(proof, err)
	}
	afterSibling, _ := runtime.Task(ctx, sibling.ID)
	if afterSibling.Revision != sibling.Revision || afterSibling.Status != sibling.Status || afterSibling.ControlState != sibling.ControlState {
		t.Fatal("cancelling exact task changed an independent sibling")
	}
	if repeated, err := binding.Execute(ctx, confirmation.Request()); err != nil || !repeated.Succeeded() {
		t.Fatal("canonical replay was not idempotent", repeated, err)
	}
	afterReplay, _ := runtime.Task(ctx, proof.ID)
	if afterReplay.Revision != proof.Revision {
		t.Fatal("exact replay advanced cancellation revision")
	}
	if _, err := binding.PrepareRequest(ctx, ActionRequest{Inputs: map[string]string{"task_id": proof.ID}}); err == nil {
		t.Fatal("terminal task offered for a new cancellation")
	}
	// Reopening a second runtime verifies the persisted control state rather
	// than the menu's cached result. No provider or worker launch is needed.
	reopened, err := app.Open(ctx, runtime.ProjectRoot())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restored, err := reopened.Task(ctx, proof.ID)
	if err != nil || restored.ID != proof.ID || restored.Revision != proof.Revision || restored.ControlState != "cancelled" || restored.Status != model.TaskCancelled {
		t.Fatal("fresh runtime lost cancellation", restored, err)
	}
}

// The shared confirmation/key tests use the legacy CancelTask fixture. Expose
// only cancellation on that fixture for Extreme's canonical typed adapter;
// other task-control tests deliberately use their own narrower authorities.
func (a *fakeAuthority) CommandTask(ctx context.Context, envelope app.CommandEnvelope, command app.TaskCommand) (model.Task, error) {
	before, err := a.Task(ctx, envelope.TargetID)
	if err != nil {
		return model.Task{}, err
	}
	if command.Operation != "cancel" || before.ID != envelope.TargetID || envelope.IdempotencyKey == "" {
		return model.Task{}, model.ErrConflict
	}
	if err := a.CancelTask(ctx, envelope.TargetID, envelope.ExpectedVersion); err != nil {
		return model.Task{}, err
	}
	a.mu.Lock()
	a.task.ControlState = "cancelled"
	a.task.OwnerAgentID = nil
	a.mu.Unlock()
	return a.Task(ctx, envelope.TargetID)
}

func TestExtremeMenuCancelRejectsForeignProjectAndRevokedAuthority(t *testing.T) {
	for _, scenario := range []string{"foreign-project", "revoked"} {
		t.Run(scenario, func(t *testing.T) {
			ws, runtime := realControlWorkspace(t, "SESSION-menu-cancel-denied")
			ctx := t.Context()
			if _, err := runtime.ImportTasks(ctx, []model.Task{{ID: "TASK-denied", Title: "untouched", Status: model.TaskReady, Risk: model.R1}}); err != nil {
				t.Fatal(err)
			}
			source := ws.controlSource()
			before, _ := runtime.Task(ctx, "TASK-denied")
			project := source.ProjectID
			if scenario == "foreign-project" {
				project = "PROJECT-foreign"
			} else {
				grants, err := runtime.Store().ListCapabilityGrants(ctx)
				if err != nil {
					t.Fatal(err)
				}
				count := 0
				for _, grant := range grants {
					if len(grant.Scope.Actions) == 1 && grant.Scope.Actions[0] == "task.control" {
						if err := runtime.Store().RevokeCapabilityGrant(ctx, grant.ID, time.Now().UTC()); err != nil {
							t.Fatal(err)
						}
						count++
					}
				}
				if count != 1 {
					t.Fatal("fixture did not revoke the exact task-control capability")
				}
				// A newly composed menu must not recreate a revoked grant.
				source = ws.controlSource()
			}
			binding := source.Bindings()["CTUI-0046"]
			confirmation := &Confirmation{}
			if err := confirmation.Begin(ctx, binding, ActionRequest{Action: binding.Action, ProjectID: project,
				SessionID: source.SessionID, Inputs: map[string]string{"task_id": before.ID}}); err != nil {
				t.Fatal(err)
			}
			confirmation.MoveSelection(1)
			confirmation.Acknowledge()
			outcome, err := confirmation.Submit(ctx)
			if !errors.Is(err, authz.ErrDenied) || outcome.Succeeded() || outcome.Verdict != VerdictFail {
				t.Fatal("denied cancellation did not report refusal", outcome, err)
			}
			after, err := runtime.Task(ctx, before.ID)
			if err != nil || after.ID != before.ID || after.Revision != before.Revision || after.Status != before.Status || after.ControlState != before.ControlState {
				t.Fatal("denied cancellation mutated durable state", after, err)
			}
		})
	}
}

type menuTaskAuthority struct {
	*fakeAuthority
	commands  int
	unchanged bool
}

func (a *menuTaskAuthority) CommandTask(_ context.Context, envelope app.CommandEnvelope, command app.TaskCommand) (model.Task, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.commands++
	if envelope.TargetID != a.task.ID || envelope.ExpectedVersion != a.task.Revision || envelope.IdempotencyKey == "" {
		return model.Task{}, model.ErrConflict
	}
	if !a.unchanged {
		a.task.Revision++
		a.task.ControlState = "pause-requested"
	}
	return a.task, nil
}

func TestExtremeMenuTaskControlsStaleUnprovenAndSettling(t *testing.T) {
	for _, scenario := range []string{"stale", "unproven", "settling"} {
		t.Run(scenario, func(t *testing.T) {
			source, original := testControl(t)
			authority := &menuTaskAuthority{fakeAuthority: original, unchanged: scenario == "unproven"}
			authority.task = model.Task{ID: "TASK-menu", Title: "menu", Status: model.TaskWorking, Revision: 4, Attempt: 1}
			source.Authority = authority
			binding := source.Bindings()["CTUI-0043"]
			confirmation := &Confirmation{}
			if err := confirmation.Begin(t.Context(), binding, ActionRequest{Action: binding.Action, ProjectID: source.ProjectID,
				SessionID: source.SessionID, Inputs: map[string]string{"task_id": "TASK-menu"}}); err != nil {
				t.Fatal(err)
			}
			if scenario == "stale" {
				authority.task.Revision++
			}
			confirmation.MoveSelection(1)
			outcome, err := confirmation.Submit(t.Context())
			switch scenario {
			case "stale":
				if !errors.Is(err, ErrStaleTarget) || authority.commands != 0 {
					t.Fatal("stale selection reached canonical mutation")
				}
			case "unproven":
				if err != nil || outcome.Succeeded() || outcome.Verdict != VerdictUnknown {
					t.Fatal("unchanged durable task was reported successful")
				}
			case "settling":
				if err != nil || !outcome.Succeeded() || !strings.Contains(outcome.Detail, "still settling") {
					t.Fatal("pause-requested was reported as a stopped worker")
				}
			}
		})
	}
}

func TestExtremeMenuTaskControlRequiresTypedTargetAndAuthority(t *testing.T) {
	source, _ := testControl(t)
	source.Authority = struct{ ControlAuthority }{source.Authority}
	binding := source.Bindings()["CTUI-0043"]
	if _, err := binding.PrepareRequest(t.Context(), ActionRequest{Inputs: map[string]string{"task_id": "TASK-menu"}}); err == nil {
		t.Fatal("authority without task control was treated as operational")
	}
	for _, id := range []string{"", "TASK menu", strings.Repeat("a", 257), "TASK\x00"} {
		if validateTaskControlID(id) == nil {
			t.Fatal("unbounded/untyped task ID accepted")
		}
	}
}

func TestExtremeBudgetSettersRequireCanonicalAuthority(t *testing.T) {
	view, _ := testControlView(t)
	ia, _ := FrozenIA()
	for _, id := range []string{"CTUI-0077", "CTUI-0078", "CTUI-0081", "CTUI-0082"} {
		node, _ := ia.Node(id)
		availability := view.buildActionAvailability(node)
		if availability.Enabled || availability.Reason != fmt.Sprintf(budgetContractOwner, id) {
			t.Fatalf("%s misleading budget affordance: %+v", id, availability)
		}
	}
}

func TestExtremeBudgetSettersOpenCanonicalForms(t *testing.T) {
	for _, entry := range []string{"menu", "palette"} {
		for _, id := range []string{"CTUI-0077", "CTUI-0078", "CTUI-0081", "CTUI-0082"} {
			t.Run(entry+"/"+id, func(t *testing.T) {
				view, base := testControlView(t)
				goal := model.GoalContract{ID: "budget-goal", SessionID: "sess-1", ProjectID: "proj-1", Revision: 1, Confirmation: model.ConfirmationApproved, RequestDigest: "digest"}
				base.goal = goal
				authority := &goalBudgetFake{fakeAuthority: base, revisions: map[int64]model.GoalContract{1: goal}}
				view.control.source.Authority = authority
				node, _ := view.Nav().ia.Node(id)
				if node.Binding != BindingBound || !strings.Contains(strings.Join(node.BackendSources, " "), "goal_budget.go") {
					t.Fatal("budget manifest does not identify the canonical setter")
				}
				if availability := view.buildActionAvailability(node); !availability.Enabled {
					t.Fatal("canonical budget setter disabled:", availability.Reason)
				}
				if entry == "palette" {
					view.HandleKey(t.Context(), runeKey('/'))
					for _, r := range node.MenuPath {
						view.HandleKey(t.Context(), runeKey(r))
					}
					view.HandleKey(t.Context(), key(KeyEnter))
				} else if err := view.Nav().DeepLink(id); err != nil {
					t.Fatal(err)
				}
				view.HandleKey(t.Context(), key(KeyEnter))
				view.HandleKey(t.Context(), key(KeyEnter))
				if !view.formOpen() || view.confirmOpen() || authority.patchCalls != 0 {
					t.Fatal("budget action did not open its input form before mutation")
				}
			})
		}
	}
}

func TestExtremeUnavailableBudgetKeysNeverOpenFormOrConfirmation(t *testing.T) {
	for _, entry := range []string{"menu", "palette"} {
		for _, id := range []string{"CTUI-0077", "CTUI-0078", "CTUI-0079", "CTUI-0080", "CTUI-0081", "CTUI-0082"} {
			t.Run(entry+"/"+id, func(t *testing.T) {
				view, _ := testControlView(t)
				view.OpenAndWait(t.Context())
				node, _ := view.Nav().ia.Node(id)
				if entry == "palette" {
					view.HandleKey(t.Context(), runeKey('/'))
					for _, r := range node.MenuPath {
						view.HandleKey(t.Context(), runeKey(r))
					}
					view.HandleKey(t.Context(), key(KeyEnter))
				} else {
					if err := view.Nav().DeepLink(id); err != nil {
						t.Fatal(err)
					}
				}
				if selected, ok := view.Nav().SelectedChild(); !ok || selected.SpecID != id {
					t.Fatal("did not select exact requested action", id)
				}
				availability := view.buildActionAvailability(node)
				if availability.Enabled {
					t.Fatal("fixture unexpectedly supports this budget", id)
				}
				for range 3 {
					view.HandleKey(t.Context(), key(KeyEnter))
				}
				if view.formOpen() || view.confirmOpen() {
					t.Fatal("unavailable action entered the form/confirmation path")
				}
				if !strings.Contains(view.status, availability.Reason) {
					t.Fatalf("dispatch did not retain the visible refusal: %q", view.status)
				}
			})
		}
	}
}

func TestExtremeStatusGapFieldsSurviveModelsSnapshotDispatch(t *testing.T) {
	view, _ := testControlView(t)
	view.control.modelsValid = true
	view.snapValid = true
	view.snap.lifecycle = lifecycleSnapshot{State: []Field{{Label: "Retained run", Value: Empty(lifecycleViewSource)}}, Timeline: []Field{{Label: "Retained run", Value: Empty(lifecycleViewSource)}}}
	for _, id := range []string{"CTUI-0092", "CTUI-0100"} {
		if err := view.Nav().DeepLink(id); err != nil {
			t.Fatal(err)
		}
		text := strings.Join(view.Render(180, 42), "\n")
		if !strings.Contains(text, "IMPLEMENTATION GAP") || !strings.Contains(text, "Retained run") || !strings.Contains(text, "EMPTY") {
			t.Fatalf("%s discarded canonical Status fields when Models was attached:\n%s", id, text)
		}
	}
	statusGap, _ := view.Nav().ia.Node("CTUI-0148")
	if _, captured := RenderModelsNode(statusGap, ModelsSnapshot{}); captured {
		t.Fatal("Models captured a Status-owned provider quota gap")
	}
	modelsGap, _ := view.Nav().ia.Node("CTUI-0557")
	if sectionTitleOf(modelsGap) != "Models" {
		t.Fatal("fixture does not represent a Models-owned gap")
	}
	if content, captured := RenderModelsNode(modelsGap, ModelsSnapshot{}); !captured || !content.HasNotice || !content.ReadOnly {
		t.Fatal("Models lost its own honest gap", content, captured)
	}
}

// A form field that asks for a cell, snapshot or recovery ID refuses bad
// input in its own words, not as a "task ID" (e2e 2026-11-06: Memory restore
// answered a snapshot ID with "a bounded canonical task ID ... is required").
func TestCanonicalIDRefusalNamesTheField(t *testing.T) {
	for what, check := range map[string]func(string) error{
		"task ID":            validateTaskControlID,
		"cell ID":            canonicalIDValidator("cell ID"),
		"memory snapshot ID": canonicalIDValidator("memory snapshot ID"),
	} {
		err := check("zz/q?bad id")
		if err == nil || !strings.Contains(err.Error(), "canonical "+what+" ") {
			t.Fatalf("%s: got %v", what, err)
		}
		if err := check("e2e-nonexistent-1"); err != nil {
			t.Fatalf("%s: a well-formed id was refused: %v", what, err)
		}
	}
}
