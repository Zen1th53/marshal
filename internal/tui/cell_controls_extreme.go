//go:build !governed

package tui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/cell"
)

type approvedCellAuthority interface {
	PreviewApprovedCell(context.Context, string, string, string, int) (cell.ApprovedCheckDescriptor, error)
	ReadApprovedCell(context.Context, string, string, cell.CellID) (cell.ApprovedCheckDescriptor, error)
	CommandPrepareApprovedCell(context.Context, app.CommandEnvelope, string, int) (cell.ApprovedCheckDescriptor, error)
	CommandExecuteApprovedCell(context.Context, app.CommandEnvelope) (cell.ApprovedCheckDescriptor, error)
	CommandDestroyApprovedCell(context.Context, app.CommandEnvelope) (cell.ApprovedCheckDescriptor, error)
}

func (a *runtimeControlAuthority) PreviewApprovedCell(ctx context.Context, project, session, task string, index int) (cell.ApprovedCheckDescriptor, error) {
	if a.runtime == nil || a.localControl == nil {
		return cell.ApprovedCheckDescriptor{}, errNoRuntime
	}
	return a.runtime.PreviewApprovedCell(a.localControl.Context(ctx), project, session, task, index)
}
func (a *runtimeControlAuthority) ReadApprovedCell(ctx context.Context, project, session string, id cell.CellID) (cell.ApprovedCheckDescriptor, error) {
	if a.runtime == nil || a.localControl == nil {
		return cell.ApprovedCheckDescriptor{}, errNoRuntime
	}
	return a.runtime.ReadApprovedCell(a.localControl.Context(ctx), project, session, id)
}
func (a *runtimeControlAuthority) CommandPrepareApprovedCell(ctx context.Context, e app.CommandEnvelope, task string, index int) (cell.ApprovedCheckDescriptor, error) {
	if a.runtime == nil || a.localControl == nil {
		return cell.ApprovedCheckDescriptor{}, errNoRuntime
	}
	return a.runtime.CommandPrepareApprovedCell(a.localControl.Context(ctx), e, task, index)
}
func (a *runtimeControlAuthority) CommandExecuteApprovedCell(ctx context.Context, e app.CommandEnvelope) (cell.ApprovedCheckDescriptor, error) {
	if a.runtime == nil || a.localControl == nil {
		return cell.ApprovedCheckDescriptor{}, errNoRuntime
	}
	return a.runtime.CommandExecuteApprovedCell(a.localControl.Context(ctx), e)
}
func (a *runtimeControlAuthority) CommandDestroyApprovedCell(ctx context.Context, e app.CommandEnvelope) (cell.ApprovedCheckDescriptor, error) {
	if a.runtime == nil || a.localControl == nil {
		return cell.ApprovedCheckDescriptor{}, errNoRuntime
	}
	return a.runtime.CommandDestroyApprovedCell(a.localControl.Context(ctx), e)
}

func parseCellIndex(value string) (int, error) {
	index, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || index < 0 || index >= 128 {
		return 0, fmt.Errorf("an approved check index from 0 to 127 is required")
	}
	return index, nil
}
func approvedCellDigest(d cell.ApprovedCheckDescriptor) string {
	raw, _ := json.Marshal(d)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// approvedCellScope marks targets that belong to the one-shot approved-check
// descriptor mechanism. Any other cell target belongs to the run-owned cell
// command mechanism that CTUI-0051/0052 also expose.
const approvedCellScope = "approved verification check; read-only canonical worktree; deny-by-default egress; no persistent VM"

func configureApprovedCellBindings(source *ControlSource, bindings map[ActionID]Binding) {
	if _, ok := source.Authority.(approvedCellAuthority); !ok {
		return
	}
	for _, id := range []ActionID{"CTUI-0050", "CTUI-0051", "CTUI-0052"} {
		binding := bindings[id]
		// The run-owned cell command (reviewed workspace action or destruction
		// against an approval) is reached through the same action id: the
		// approved-check descriptor mechanism stays canonical for descriptor
		// ids, and only a cell it does not own falls through to that command.
		runOwned := binding
		binding.Gap = ""
		binding.Prepare = nil
		binding.RequiresApproval = false
		if id == "CTUI-0050" {
			binding.Inputs = []InputSpec{{Key: "task_id", Label: "Canonical task ID with its existing approved-plan worktree", Required: true, Validate: validateTaskControlID}, {Key: "check_index", Label: "Approved check index (0 is the first check)", Required: true, Validate: func(value string) error { _, err := parseCellIndex(value); return err }}}
		} else {
			cellInput := InputSpec{Key: "cell_id", Label: "Exact cell ID (one-shot descriptor returned by Prepare, or a run-owned execution cell)", Required: true, Validate: canonicalIDValidator("cell ID")}
			if _, hasRunOwned := source.Authority.(cellCommandAuthority); hasRunOwned && runOwned.PrepareRequest != nil && len(runOwned.Inputs) > 0 {
				binding.Inputs = []InputSpec{cellInput}
				for _, input := range runOwned.Inputs[1:] {
					input.Required = false
					input.Label += " (run-owned cell only)"
					binding.Inputs = append(binding.Inputs, input)
				}
			} else {
				binding.Inputs = []InputSpec{cellInput}
				runOwned = Binding{}
			}
		}
		binding.PrepareRequest = func(ctx context.Context, req ActionRequest) (Target, error) {
			if id != "CTUI-0050" && runOwned.PrepareRequest != nil {
				target, err := source.prepareApprovedCell(ctx, req, id)
				if err == nil {
					return target, nil
				}
				if fallback, fallbackErr := runOwned.PrepareRequest(ctx, req); fallbackErr == nil {
					return fallback, nil
				}
				return Target{}, err
			}
			return source.prepareApprovedCell(ctx, req, id)
		}
		binding.Execute = func(ctx context.Context, req ActionRequest) (Outcome, error) {
			if id != "CTUI-0050" && runOwned.Execute != nil && req.Target.Scope != approvedCellScope {
				if req.ApprovalID == "" {
					req.ApprovalID = strings.TrimSpace(req.Inputs["approval_id"])
				}
				return runOwned.Execute(ctx, req)
			}
			return source.executeApprovedCell(ctx, req, id)
		}
		bindings[id] = binding
	}
}

func (s *ControlSource) prepareApprovedCell(ctx context.Context, req ActionRequest, id ActionID) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	authority, ok := s.Authority.(approvedCellAuthority)
	if !ok {
		return Target{}, ErrNoBinding
	}
	var d cell.ApprovedCheckDescriptor
	var err error
	revision := int64(0)
	if id == "CTUI-0050" {
		task := strings.TrimSpace(req.Inputs["task_id"])
		if err := validateTaskControlID(task); err != nil {
			return Target{}, err
		}
		index, err := parseCellIndex(req.Inputs["check_index"])
		if err != nil {
			return Target{}, err
		}
		d, err = authority.PreviewApprovedCell(ctx, s.ProjectID, s.SessionID, task, index)
	} else {
		cellID := strings.TrimSpace(req.Inputs["cell_id"])
		if err := validateCanonicalID("cell ID", cellID); err != nil {
			return Target{}, err
		}
		d, err = authority.ReadApprovedCell(ctx, s.ProjectID, s.SessionID, cell.CellID(cellID))
		revision = d.Revision
		if err == nil && string(d.ID) != cellID {
			return Target{}, ErrStaleTarget
		}
	}
	if err != nil {
		return Target{}, err
	}
	if d.ProjectID != s.ProjectID || d.SessionID != s.SessionID || d.Validate() != nil {
		return Target{}, ErrStaleTarget
	}
	if id == "CTUI-0051" && d.State != cell.StateReady {
		return Target{}, fmt.Errorf("only an unused ready descriptor can execute; current state %s", d.State)
	}
	if id == "CTUI-0052" && d.State == cell.StateDestroyed && d.Settled {
		return Target{}, fmt.Errorf("this one-shot descriptor is already settled and revoked")
	}
	detail := fmt.Sprintf("one-shot approved check %s#%d for task %s; plan %s@%d; cell %s; state %s", d.PlanTaskID, d.CheckIndex, d.TaskID, d.PlanID, d.PlanVersion, d.ID, d.State)
	return Target{Kind: "cell", ID: string(d.ID), Revision: revision, Digest: approvedCellDigest(d), Scope: approvedCellScope, Summary: detail}, nil
}

func (s *ControlSource) executeApprovedCell(ctx context.Context, req ActionRequest, id ActionID) (Outcome, error) {
	authority, ok := s.Authority.(approvedCellAuthority)
	if !ok {
		return Outcome{}, ErrNoBinding
	}
	if req.Target.Kind != "cell" {
		return Outcome{}, ErrStaleTarget
	}
	fresh, err := s.prepareApprovedCell(ctx, req, id)
	if err != nil || fresh.ID != req.Target.ID || fresh.Revision != req.Target.Revision || fresh.Digest != req.Target.Digest {
		return Outcome{}, ErrStaleTarget
	}
	e := app.CommandEnvelope{ProjectID: req.ProjectID, SessionID: req.SessionID, TargetID: "cell:" + req.Target.ID, ExpectedVersion: req.Target.Revision, IdempotencyKey: req.IdempotencyKey}
	var result cell.ApprovedCheckDescriptor
	switch id {
	case "CTUI-0050":
		index, indexErr := parseCellIndex(req.Inputs["check_index"])
		if indexErr != nil {
			return Outcome{}, indexErr
		}
		result, err = authority.CommandPrepareApprovedCell(ctx, e, strings.TrimSpace(req.Inputs["task_id"]), index)
	case "CTUI-0051":
		result, err = authority.CommandExecuteApprovedCell(ctx, e)
	case "CTUI-0052":
		result, err = authority.CommandDestroyApprovedCell(ctx, e)
	default:
		return Outcome{}, ErrNoBinding
	}
	if err != nil {
		return refusal("canonical one-shot cell operation refused", err, req.Target, "internal/app/cell_controls_extreme.go")
	}
	proof, err := authority.ReadApprovedCell(ctx, s.ProjectID, s.SessionID, cell.CellID(req.Target.ID))
	if err != nil {
		return unproven(req.Target, string(result.ID), err)
	}
	if proof.ID != cell.CellID(req.Target.ID) || proof.ProjectID != s.ProjectID || proof.SessionID != s.SessionID || !reflect.DeepEqual(proof, result) || proof.Validate() != nil || proof.Revision <= req.Target.Revision {
		return Outcome{Verdict: VerdictUnknown, Detail: "cell operation returned without coherent durable descriptor read-back", Target: req.Target, Proof: Unknown("refresh exact cell descriptor", "internal/app/cell_controls_extreme.go")}, nil
	}
	detail := fmt.Sprintf("cell %s retained at revision %d; state %s", proof.ID, proof.Revision, proof.State)
	if id == "CTUI-0050" {
		if proof.State != cell.StateReady || proof.Process.PID != 0 {
			return unproven(req.Target, string(proof.ID), ErrStaleTarget)
		}
		detail += "; descriptor prepared for one approved check; no process started"
	} else {
		if proof.State != cell.StateDestroyed || !proof.Settled {
			return unproven(req.Target, string(proof.ID), ErrStaleTarget)
		}
		detail += "; owned execution joined or unused descriptor revoked"
		if proof.ExitCode != nil {
			detail += fmt.Sprintf("; check exit code %d; independent task verification remains required", *proof.ExitCode)
		}
	}
	verdict := VerdictPass
	if id == "CTUI-0051" && proof.ExitCode != nil && *proof.ExitCode != 0 {
		verdict = VerdictFail
	}
	return Outcome{Verdict: verdict, Detail: detail, Target: Target{Kind: "cell", ID: string(proof.ID), Revision: proof.Revision}, Proof: Known(detail, "internal/store/cell_descriptor_extreme.go")}, nil
}
