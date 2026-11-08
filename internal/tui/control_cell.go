package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/cell"
)

type cellCommandAuthority interface {
	InspectCellCommand(context.Context, cell.CellID, string) (app.CellCommandTarget, error)
	CommandExecCell(context.Context, app.CommandEnvelope, app.CellExecCommand) (app.CellCommandProof, error)
	CommandDestroyCell(context.Context, app.CommandEnvelope, app.CellDestroyCommand) (app.CellCommandProof, error)
}

func (a *runtimeControlAuthority) InspectCellCommand(ctx context.Context, id cell.CellID, session string) (app.CellCommandTarget, error) {
	if a == nil || a.runtime == nil || a.localControl == nil {
		return app.CellCommandTarget{}, ErrNoBinding
	}
	if session != a.sessionID {
		return app.CellCommandTarget{}, ErrStaleTarget
	}
	return a.runtime.InspectCellCommand(ctx, id, session)
}
func (a *runtimeControlAuthority) CommandExecCell(ctx context.Context, e app.CommandEnvelope, input app.CellExecCommand) (app.CellCommandProof, error) {
	if a == nil || a.runtime == nil || a.localControl == nil {
		return app.CellCommandProof{}, ErrNoBinding
	}
	if e.ProjectID != a.runtime.ProjectIdentity() || e.SessionID != a.sessionID {
		return app.CellCommandProof{}, ErrStaleTarget
	}
	return a.runtime.CommandExecCell(a.localControl.Context(ctx), e, input)
}
func (a *runtimeControlAuthority) CommandDestroyCell(ctx context.Context, e app.CommandEnvelope, input app.CellDestroyCommand) (app.CellCommandProof, error) {
	if a == nil || a.runtime == nil || a.localControl == nil {
		return app.CellCommandProof{}, ErrNoBinding
	}
	if e.ProjectID != a.runtime.ProjectIdentity() || e.SessionID != a.sessionID {
		return app.CellCommandProof{}, ErrStaleTarget
	}
	return a.runtime.CommandDestroyCell(a.localControl.Context(ctx), e, input)
}

func cellControlTarget(t app.CellCommandTarget) Target {
	return Target{Kind: "cell", ID: string(t.Cell.ID), Revision: t.TaskRevision, Digest: t.Digest(), Scope: t.Cell.Workspace, Summary: fmt.Sprintf("cell %s (%s), task %s revision %d, run %s revision %d", t.Cell.ID, t.Cell.State, t.Cell.TaskID, t.TaskRevision, t.RunID, t.RunVersion)}
}

func cellCommandBindings(s *ControlSource) map[ActionID]Binding {
	return map[ActionID]Binding{"CTUI-0051": cellCommandBinding(s, "CTUI-0051", false), "CTUI-0052": cellCommandBinding(s, "CTUI-0052", true)}
}

func cellCommandBinding(s *ControlSource, id ActionID, destroy bool) Binding {
	b := Binding{Action: id, Title: "execute reviewed cell action", Safety: SafetyGoverned, RequiresApproval: true,
		Inputs: []InputSpec{{Key: "cell_id", Label: "Exact cell ID", Required: true}, {Key: "action", Label: "Action (workspace.status or workspace.diff)", Default: string(app.CellWorkspaceStatus), Required: true, Validate: func(value string) error {
			if app.CellAction(value) != app.CellWorkspaceStatus && app.CellAction(value) != app.CellWorkspaceDiff {
				return fmt.Errorf("choose workspace.status or workspace.diff")
			}
			return nil
		}}, {Key: "approval_id", Label: "Exact canonical action approval ID", Required: true}},
	}
	if destroy {
		b.Title = "destroy execution cell"
		b.Safety = SafetyDestructive
		b.Inputs = []InputSpec{{Key: "cell_id", Label: "Exact cell ID", Required: true}, {Key: "reason", Label: "Reason for destruction", Required: true}, {Key: "approval_id", Label: "Exact canonical destruction approval ID", Required: true}}
	}
	b.PrepareRequest = func(ctx context.Context, req ActionRequest) (Target, error) {
		if s == nil {
			return Target{}, ErrNoBinding
		}
		a, ok := s.Authority.(cellCommandAuthority)
		if !ok {
			return Target{}, ErrNoBinding
		}
		id := strings.TrimSpace(req.Inputs["cell_id"])
		if id == "" {
			return Target{}, fmt.Errorf("select an exact cell ID")
		}
		target, err := a.InspectCellCommand(ctx, cell.CellID(id), s.SessionID)
		if err != nil {
			return Target{}, err
		}
		if string(target.Cell.ID) != id {
			return Target{}, ErrStaleTarget
		}
		return cellControlTarget(target), nil
	}
	b.Execute = func(ctx context.Context, req ActionRequest) (Outcome, error) {
		const source = "internal/app/cell_commands.go"
		if s == nil {
			return refusal("cell authority unavailable", ErrNoBinding, req.Target, source)
		}
		a, ok := s.Authority.(cellCommandAuthority)
		if !ok {
			return refusal("cell authority unavailable", ErrNoBinding, req.Target, source)
		}
		if req.Target.Kind != "cell" || req.ProjectID != s.ProjectID || req.SessionID != s.SessionID || req.ApprovalID == "" || req.ApprovalID != req.Inputs["approval_id"] {
			return refusal("cell request binding mismatch", ErrStaleTarget, req.Target, source)
		}
		target, err := a.InspectCellCommand(ctx, cell.CellID(req.Target.ID), req.SessionID)
		if err != nil {
			return refusal("cell read failed", err, req.Target, source)
		}
		if target.Digest() != req.Target.Digest || target.TaskRevision != req.Target.Revision {
			return refusal("cell lifecycle changed", ErrStaleTarget, req.Target, source)
		}
		e := app.CommandEnvelope{ProjectID: req.ProjectID, SessionID: req.SessionID, TargetID: req.Target.ID, ExpectedVersion: req.Target.Revision, IdempotencyKey: req.IdempotencyKey}
		var proof app.CellCommandProof
		if destroy {
			proof, err = a.CommandDestroyCell(ctx, e, app.CellDestroyCommand{Target: target, ApprovalID: req.ApprovalID, Reason: req.Inputs["reason"]})
		} else {
			proof, err = a.CommandExecCell(ctx, e, app.CellExecCommand{Target: target, Action: app.CellAction(req.Inputs["action"]), ApprovalID: req.ApprovalID})
		}
		if proof.Outcome == "UNKNOWN" {
			reason := "cell intent is durable; external outcome is UNKNOWN and requires reconciliation"
			if proof.Cell.State != "" {
				reason += "; durable cell state=" + string(proof.Cell.State)
			}
			if err != nil {
				reason += "; " + err.Error()
			}
			return Outcome{Verdict: VerdictUnknown, Detail: reason, Target: req.Target, Proof: Unknown(reason, source)}, nil
		}
		if err != nil {
			return refusal("cell command refused", err, req.Target, source)
		}
		if proof.Cell.ID != target.Cell.ID || destroy && (proof.Outcome != "DESTROYED" || proof.Cell.State != cell.StateDestroyed) || !destroy && (proof.Outcome != "EXECUTED" || proof.ExitCode == nil) {
			return unproven(req.Target, req.Target.ID, fmt.Errorf("cell command has no durable result"))
		}
		detail := "cell destruction proved by durable destroyed state"
		if !destroy {
			detail = fmt.Sprintf("reviewed cell action executed; durable exit code %d", *proof.ExitCode)
			if *proof.ExitCode != 0 {
				return Outcome{Verdict: VerdictFail, Detail: detail, Target: req.Target, Proof: Known(detail, source)}, nil
			}
		}
		return Outcome{Verdict: VerdictPass, Detail: detail, Target: req.Target, Proof: Known(detail, source)}, nil
	}
	return b
}
