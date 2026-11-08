//go:build !governed

package tui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/cell"
)

type menuCellAuthority struct {
	*fakeAuthority
	descriptor        cell.ApprovedCheckDescriptor
	calls             int
	wrongProof        bool
	wrongAfterCommand bool
}

func (a *menuCellAuthority) PreviewApprovedCell(context.Context, string, string, string, int) (cell.ApprovedCheckDescriptor, error) {
	return a.descriptor, nil
}
func (a *menuCellAuthority) ReadApprovedCell(context.Context, string, string, cell.CellID) (cell.ApprovedCheckDescriptor, error) {
	d := a.descriptor
	if a.wrongProof {
		d.SessionID = "foreign-session"
	}
	return d, nil
}
func (a *menuCellAuthority) CommandPrepareApprovedCell(_ context.Context, e app.CommandEnvelope, task string, index int) (cell.ApprovedCheckDescriptor, error) {
	a.calls++
	return a.descriptor, nil
}
func (a *menuCellAuthority) CommandExecuteApprovedCell(_ context.Context, e app.CommandEnvelope) (cell.ApprovedCheckDescriptor, error) {
	a.calls++
	a.descriptor.Revision++
	a.descriptor.State = cell.StateDestroyed
	a.descriptor.Settled = true
	if a.wrongAfterCommand {
		a.wrongProof = true
	}
	return a.descriptor, nil
}
func (a *menuCellAuthority) CommandDestroyApprovedCell(_ context.Context, e app.CommandEnvelope) (cell.ApprovedCheckDescriptor, error) {
	return a.CommandExecuteApprovedCell(context.Background(), e)
}

func TestExtremeCellControlsUseSelectorsAndRequireDurableReadback(t *testing.T) {
	view, _ := testControlView(t)
	source := view.control.source
	original := source.Authority.(*fakeAuthority)
	spec := cell.Spec{TaskID: "TASK-cell", Workspace: "/tmp/cell", Backend: cell.BackendBubblewrap, NetworkProfile: "deny-by-default"}
	id, err := cell.CellIdentity(spec)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("true"))
	authority := &menuCellAuthority{fakeAuthority: original, descriptor: cell.ApprovedCheckDescriptor{ID: id, ProjectID: source.ProjectID, SessionID: source.SessionID, TaskID: "TASK-cell", TaskRevision: 1, GoalID: "GOAL-cell", GoalRevision: 1, PlanID: "PLAN-cell", PlanVersion: 1, PlanTaskID: "check", CheckIndex: 0, CheckDigest: hex.EncodeToString(sum[:]), ApprovalScopeDigest: "sha256:scope", Spec: spec, Revision: 1, State: cell.StateReady}}
	source.Authority = authority
	bindings := source.Bindings()
	for _, action := range []ActionID{"CTUI-0050", "CTUI-0051", "CTUI-0052"} {
		if !bindings[action].Bound() {
			t.Fatalf("implemented cell %s missing typed binding", action)
		}
	}
	req := ActionRequest{Action: "CTUI-0050", ProjectID: source.ProjectID, SessionID: source.SessionID, Inputs: map[string]string{"task_id": "TASK-cell", "check_index": "0"}, IdempotencyKey: "prepare-cell"}
	req.Target, err = bindings[req.Action].PrepareRequest(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if req.Target.Kind != "cell" || req.Target.Revision != 0 {
		t.Fatalf("preparation target %+v", req.Target)
	}
	outcome, err := bindings[req.Action].Execute(t.Context(), req)
	if err != nil || outcome.Verdict != VerdictPass || authority.calls != 1 {
		t.Fatalf("prepare %+v %v calls%d", outcome, err, authority.calls)
	}
	req.Action = "CTUI-0052"
	req.Inputs = map[string]string{"cell_id": string(id)}
	req.IdempotencyKey = "destroy-cell"
	req.Target, err = bindings[req.Action].PrepareRequest(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	authority.wrongAfterCommand = true
	outcome, err = bindings[req.Action].Execute(t.Context(), req)
	if err != nil || outcome.Verdict != VerdictUnknown {
		t.Fatalf("foreign readback fabricated settlement %+v %v", outcome, err)
	}
}

func TestExtremeCellMenuAndPaletteOpenTypedFormsOnlyWithAuthority(t *testing.T) {
	for _, entry := range []string{"menu", "palette"} {
		for _, id := range []string{"CTUI-0050", "CTUI-0051", "CTUI-0052"} {
			t.Run(entry+"/"+id, func(t *testing.T) {
				view, _ := testControlView(t)
				view.OpenAndWait(t.Context())
				view.control.source.Authority = &menuCellAuthority{fakeAuthority: view.control.source.Authority.(*fakeAuthority)}
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
				view.HandleKey(t.Context(), key(KeyEnter))
				if !view.formOpen() || view.confirmOpen() {
					t.Fatal("canonical cell action did not open typed selector form")
				}
				view.HandleKey(t.Context(), key(KeyEsc))
				if view.formOpen() {
					t.Fatal("cell selector form failed to close")
				}
				view.control.source.Authority = view.control.source.Authority.(*menuCellAuthority).fakeAuthority
				if availability := view.buildActionAvailability(node); availability.Enabled {
					t.Fatal("authority without private cell API advertised action")
				}
			})
		}
	}
}
