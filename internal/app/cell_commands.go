package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/Zen1th53/marshal/internal/statepath"
	"path/filepath"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/cell"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

// CellAction is a closed tool vocabulary. It never accepts executable paths,
// shell text, arbitrary argv, environment, secret references or network scope.
type CellAction string

const (
	CellWorkspaceStatus CellAction = "workspace.status"
	CellWorkspaceDiff   CellAction = "workspace.diff"
)

type CellCommandTarget struct {
	Cell         cell.Record        `json:"cell"`
	TaskRevision int64              `json:"task_revision"`
	RunID        string             `json:"run_id"`
	RunVersion   int64              `json:"run_version"`
	RunState     execution.RunState `json:"run_state"`
	Binding      store.TaskBinding  `json:"binding"`
}

func (t CellCommandTarget) Digest() string {
	b, _ := json.Marshal(t)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// sameCellExecutionAfterApproval recognizes the one canonical run transition
// caused by Engine.DecideApproval. It tolerates exactly the resulting run
// version increment while requiring every reviewed execution field to match.
func sameCellExecutionAfterApproval(reviewed, current CellCommandTarget) bool {
	if current.RunVersion == reviewed.RunVersion {
		return current.Digest() == reviewed.Digest()
	}
	if current.RunVersion != reviewed.RunVersion+1 {
		return false
	}
	current.RunVersion = reviewed.RunVersion
	return current.Digest() == reviewed.Digest()
}

type CellExecCommand struct {
	Target     CellCommandTarget
	Action     CellAction
	ApprovalID string
}
type CellDestroyCommand struct {
	Target             CellCommandTarget
	ApprovalID, Reason string
}

// CellCommandProof contains bounded durable observations, never process output.
// UNKNOWN means an intent may have reached the backend and must not be replayed.
type CellCommandProof struct {
	Cell     cell.Record
	Outcome  string
	ExitCode *int
	Replayed bool
}

func cellActionRequest(action CellAction) (cell.ExecRequest, error) {
	base := []string{"/usr/bin/git", "--no-optional-locks", "-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false"}
	switch action {
	case CellWorkspaceStatus:
		base = append(base, "status", "--porcelain=v1", "--untracked-files=no")
	case CellWorkspaceDiff:
		base = append(base, "diff", "--no-ext-diff", "--no-textconv", "--stat")
	default:
		return cell.ExecRequest{}, model.ErrInvalid
	}
	return cell.ExecRequest{Command: base, Env: map[string]string{"GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": "/dev/null", "GIT_TERMINAL_PROMPT": "0", "GIT_PAGER": "cat", "LC_ALL": "C"}}, nil
}

// InspectCellCommand derives ownership from canonical task/run records. The
// task revision serves as the envelope version; the full cell lifecycle,
// handle/spec and run version are additionally bound by Target.Digest().
func (r *Runtime) InspectCellCommand(ctx context.Context, id cell.CellID, session string) (CellCommandTarget, error) {
	if r == nil || r.store == nil {
		return CellCommandTarget{}, model.ErrUnavailable
	}
	record, err := r.store.GetCell(ctx, id)
	if err != nil {
		return CellCommandTarget{}, err
	}
	task, err := r.store.GetTask(ctx, string(record.TaskID))
	if err != nil {
		return CellCommandTarget{}, err
	}
	binding, err := r.store.TaskExecutionBinding(ctx, task.ID)
	if err != nil {
		return CellCommandTarget{}, err
	}
	if binding.SessionID != session {
		return CellCommandTarget{}, authz.ErrDenied
	}
	if binding.GoalID == "" || binding.GoalRevision < 1 {
		return CellCommandTarget{}, fmt.Errorf("%w: cell has no canonical Goal binding", model.ErrConflict)
	}
	// Workspaces must be canonical task worktrees, never an operator-supplied
	// path or merely any directory within the project.
	if task.Worktree == nil || *task.Worktree != record.Workspace {
		return CellCommandTarget{}, model.ErrConflict
	}
	workspace, err := filepath.EvalSymlinks(record.Workspace)
	if err != nil || workspace != record.Workspace {
		return CellCommandTarget{}, model.ErrConflict
	}
	// A long-lived ExecutionService holds a process-local run projection. Open
	// the canonical atomic-file store afresh so another runtime's cancellation
	// or revision change cannot be hidden by that projection.
	runStore, err := execution.NewFileRunStore(statepath.Path(r.layout.Root, "execution", "runs"))
	if err != nil {
		return CellCommandTarget{}, err
	}
	runs, err := runStore.ListRuns(ctx)
	if err != nil {
		return CellCommandTarget{}, err
	}
	var found *execution.ExecutionRun
	for i := range runs {
		run := &runs[i]
		if string(run.ProjectID) != r.ProjectIdentity() || run.SessionID != session || run.GoalID != binding.GoalID || run.GoalRevision != binding.GoalRevision || run.PlanID != binding.PlanID || run.PlanVersion != binding.PlanVersion {
			continue
		}
		for _, candidate := range run.Tasks {
			if candidate.CanonicalTaskID == task.ID && candidate.WorktreePath == record.Workspace {
				if found != nil {
					return CellCommandTarget{}, fmt.Errorf("%w: cell maps to multiple runs", model.ErrConflict)
				}
				found = run
				break
			}
		}
	}
	if found == nil {
		return CellCommandTarget{}, fmt.Errorf("%w: no canonical run owns this cell workspace", model.ErrConflict)
	}
	return CellCommandTarget{Cell: record, TaskRevision: task.Revision, RunID: found.RunID, RunVersion: found.Version, RunState: found.State, Binding: binding}, nil
}

// CellCommandApprovalBinding is the exact canonical approval request shape.
// The payload includes fixed argv/environment and the complete reviewed target.
// This helper does not mint or decide an approval.
func CellCommandApprovalBinding(target CellCommandTarget, action CellAction, destroy bool, reason string) (execution.ApprovalRequest, error) {
	op := "cell.exec"
	var payload any
	if destroy {
		if strings.TrimSpace(reason) == "" {
			return execution.ApprovalRequest{}, model.ErrInvalid
		}
		op = "cell.destroy"
		payload = struct {
			Target CellCommandTarget
			Reason string
		}{target, reason}
	} else {
		request, err := cellActionRequest(action)
		if err != nil {
			return execution.ApprovalRequest{}, err
		}
		payload = struct {
			Target  CellCommandTarget
			Action  CellAction
			Request cell.ExecRequest
		}{target, action, request}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return execution.ApprovalRequest{}, err
	}
	return execution.ApprovalRequest{RunID: target.RunID, TaskID: string(target.Cell.TaskID), PlanID: target.Binding.PlanID, PlanVersion: target.Binding.PlanVersion, OperationType: op, TargetResource: string(target.Cell.ID), Scope: target.Cell.Workspace, Parameters: string(data), CurrentState: target.Digest()}, nil
}

// DecideCellCommandApproval is the typed decision entry point for cell
// approvals. It verifies the durable request's exact reviewed target before
// delegating to Engine.DecideApproval, whose normal run CAS increment is
// accounted for by sameCellExecutionAfterApproval at execution time.
func (r *Runtime) DecideCellCommandApproval(ctx context.Context, approvalID string, approve bool, decider, reason string) error {
	if r == nil || r.Execution() == nil || approvalID == "" {
		return model.ErrInvalid
	}
	manager := r.Execution().Engine().ApprovalManager()
	pending, err := manager.GetApproval(approvalID)
	if err != nil {
		return err
	}
	if pending.OperationType != "cell.exec" && pending.OperationType != "cell.destroy" {
		return model.ErrInvalid
	}
	if pending.RunID == "" || pending.TaskID == "" || pending.TargetResource == "" || pending.Scope == "" || pending.ActionDigest == "" || pending.StateDigest == "" {
		return model.ErrInvalid
	}
	return r.Execution().Engine().DecideApproval(ctx, approvalID, approve, decider, reason)
}

func (r *Runtime) CommandExecCell(ctx context.Context, e CommandEnvelope, input CellExecCommand) (CellCommandProof, error) {
	return r.commandCell(ctx, e, input.Target, input.Action, input.ApprovalID, false, "")
}
func (r *Runtime) CommandDestroyCell(ctx context.Context, e CommandEnvelope, input CellDestroyCommand) (CellCommandProof, error) {
	return r.commandCell(ctx, e, input.Target, "", input.ApprovalID, true, input.Reason)
}

func (r *Runtime) commandCell(ctx context.Context, e CommandEnvelope, reviewed CellCommandTarget, action CellAction, approvalID string, destroy bool, reason string) (CellCommandProof, error) {
	p, ok := auth.LocalFromContext(ctx)
	if r == nil || r.store == nil || r.cellManager == nil || !ok || ctx.Value(localControlKey{}) != r || p.ProjectID() != e.ProjectID || e.ProjectID != r.ProjectIdentity() {
		return CellCommandProof{}, authz.ErrDenied
	}
	if e.TargetID != string(reviewed.Cell.ID) || e.ExpectedVersion != reviewed.TaskRevision || e.SessionID == "" || strings.TrimSpace(e.IdempotencyKey) == "" || len(e.IdempotencyKey) > 256 || approvalID == "" {
		return CellCommandProof{}, model.ErrInvalid
	}
	op := "cell.exec"
	if destroy {
		op = "cell.destroy"
	}
	principal := authz.Principal{ID: p.ID(), Role: authz.Role{Name: "orchestrator", Authorities: []authz.Authority{authz.AuthorityTaskPlan}}}
	query := capability.Query{Subject: capability.SubjectID(p.ID()), TaskID: capability.TaskID(e.ProjectID), Kind: capability.KindFilesystemWrite, Resource: r.layout.Database, Action: op}
	decision, err := authz.CanWithCapability(ctx, principal, authz.AuthorityTaskPlan, r.layout.Database, query, capability.NewEngine(r.store, nil))
	if err != nil {
		return CellCommandProof{}, err
	}
	approval, err := CellCommandApprovalBinding(reviewed, action, destroy, reason)
	if err != nil {
		return CellCommandProof{}, err
	}
	data, _ := json.Marshal(struct {
		Envelope           CommandEnvelope
		Target             CellCommandTarget
		Action             CellAction
		ApprovalID, Reason string
	}{e, reviewed, action, approvalID, reason})
	sum := sha256.Sum256(data)
	key := sha256.Sum256([]byte(e.IdempotencyKey))
	intent := store.CommandRecord{ProjectID: e.ProjectID, Actor: p.ID(), Key: hex.EncodeToString(key[:]), Operation: op + ".intent", SessionID: e.SessionID, TargetID: e.TargetID, ExpectedVersion: e.ExpectedVersion, Digest: hex.EncodeToString(sum[:]), CapabilityGrantID: decision.CapabilityGrantID}
	resultRecord := intent
	resultRecord.Key += "/result"
	resultRecord.Operation = op + ".result"
	if _, found, err := r.store.CommandResult(ctx, intent); err != nil {
		return CellCommandProof{}, err
	} else if found {
		return r.cellCommandReadback(ctx, intent, resultRecord, destroy, true)
	}
	current, err := r.InspectCellCommand(ctx, reviewed.Cell.ID, e.SessionID)
	if err != nil {
		return CellCommandProof{}, err
	}
	if !sameCellExecutionAfterApproval(reviewed, current) {
		return CellCommandProof{}, model.ErrConflict
	}
	var request cell.ExecRequest
	if !destroy {
		request, err = cellActionRequest(action)
		if err != nil {
			return CellCommandProof{}, err
		}
	}
	result, err := r.cellManager.CommandBound(ctx, current.Cell, destroy, request, func(_ cell.Record) error {
		latest, err := r.InspectCellCommand(ctx, reviewed.Cell.ID, e.SessionID)
		if err != nil {
			return err
		}
		if !sameCellExecutionAfterApproval(reviewed, latest) {
			return model.ErrConflict
		}
		run, err := r.Execution().GetRun(ctx, latest.RunID)
		if err != nil {
			return err
		}
		if !destroy {
			if latest.RunState != execution.RunRunning {
				return model.ErrConflict
			}
			if err = r.Execution().Engine().ValidateRunGoal(ctx, run); err != nil {
				return err
			}
		}
		manager := r.Execution().Engine().ApprovalManager()
		a, err := manager.GetApproval(approvalID)
		if err != nil {
			return err
		}
		approvedBinding := approval
		if a.StateDigest != execution.ComputeStateDigest(approvedBinding.CurrentState) && reviewed.RunVersion > 1 {
			// Engine.DecideApproval advances the run CAS version. If the caller
			// refreshed the target after that decision, reconstruct the one
			// reviewed version immediately before it and bind that exact request.
			prior := reviewed
			prior.RunVersion--
			if sameCellExecutionAfterApproval(prior, reviewed) {
				if priorBinding, bindErr := CellCommandApprovalBinding(prior, action, destroy, reason); bindErr == nil {
					approvedBinding = priorBinding
				}
			}
		}
		actionDigest := execution.ComputeActionDigest(approvedBinding.OperationType, approvedBinding.TargetResource, approvedBinding.DiffPreview, approvedBinding.Parameters)
		stateDigest := execution.ComputeStateDigest(approvedBinding.CurrentState)
		if a.Status != execution.ApprovalApproved || a.HardViolation || a.RunID != latest.RunID || a.TaskID != string(latest.Cell.TaskID) || a.PlanID != latest.Binding.PlanID || a.PlanVersion != latest.Binding.PlanVersion || a.OperationType != op || a.TargetResource != e.TargetID || a.Scope != latest.Cell.Workspace || a.ActionDigest != actionDigest || a.StateDigest != stateDigest || a.ExpiresAt == nil || !time.Now().UTC().Before(*a.ExpiresAt) {
			return model.ErrApprovalRequired
		}
		if r.policy == nil {
			return model.ErrUnavailable
		}
		policyOp := model.ShellExecute
		if destroy {
			policyOp = model.DestructiveOperation
		}
		policyDecision := r.policy.Decide(model.PolicyInput{AgentID: p.ID(), SessionID: e.SessionID, Role: model.RoleOrchestrator, TaskID: string(current.Cell.TaskID), Operation: policyOp, Target: current.Cell.Workspace, TaskOwned: true, TargetInScope: true, ApprovalValid: true})
		if policyDecision.Decision != model.Allow {
			return model.ErrPolicyDenied
		}
		if err = r.store.BeginCellCommand(ctx, intent, current.Cell, current.TaskRevision, destroy); err != nil {
			return err
		}
		// Consumption follows intent persistence: a crash in either direction
		// leaves an observable UNKNOWN and never permits automatic relaunch.
		return manager.ValidateAndConsume(approvalID, actionDigest, stateDigest, time.Now().UTC())
	}, func() (func(), error) {
		return r.store.BeginGoalDispatch(ctx, current.Binding.SessionID, current.Binding.GoalID, current.Binding.GoalRevision)
	})
	if err != nil {
		if _, found, readErr := r.store.CommandResult(ctx, intent); readErr != nil {
			return CellCommandProof{}, readErr
		} else if found {
			proof, _ := r.cellCommandReadback(ctx, intent, resultRecord, destroy, false)
			return proof, fmt.Errorf("cell effect failed or ambiguous; reconciliation required: %w", err)
		}
		return CellCommandProof{}, err
	}
	version := int64(result.ExitCode) + 2
	if destroy {
		version = 2
	}
	if version < 2 || version > 257 {
		return CellCommandProof{Cell: current.Cell, Outcome: "UNKNOWN"}, fmt.Errorf("%w: backend exit code cannot be recorded", model.ErrInvalid)
	}
	resultRecord.Result = "cell-effect-observed"
	if err = r.store.RecordCommandReceipt(ctx, resultRecord, version); err != nil {
		observed, _ := r.store.GetCell(ctx, current.Cell.ID)
		return CellCommandProof{Cell: observed, Outcome: "UNKNOWN"}, err
	}
	return r.cellCommandReadback(ctx, intent, resultRecord, destroy, false)
}

func (r *Runtime) cellCommandReadback(ctx context.Context, intent, result store.CommandRecord, destroy, replay bool) (CellCommandProof, error) {
	record, err := r.store.GetCell(ctx, cell.CellID(intent.TargetID))
	if err != nil {
		return CellCommandProof{}, err
	}
	proof := CellCommandProof{Cell: record, Outcome: "UNKNOWN", Replayed: replay}
	version, found, err := r.store.CommandResult(ctx, result)
	if err != nil {
		return proof, err
	}
	if !found && destroy && record.State == cell.StateDestroyed {
		// Destruction has a durable terminal observation; execution has no
		// comparable oracle and must remain UNKNOWN after an interrupted intent.
		result.Result = "cell-destruction-reconciled"
		if err = r.store.RecordCommandReceipt(ctx, result, 2); err != nil {
			return proof, err
		}
		found = true
		version = 2
	}
	if found {
		if destroy {
			if record.State != cell.StateDestroyed {
				return proof, model.ErrConflict
			}
			proof.Outcome = "DESTROYED"
		} else {
			exit := int(version) - 2
			proof.ExitCode = &exit
			proof.Outcome = "EXECUTED"
		}
	}
	return proof, nil
}
