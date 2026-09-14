package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter/codex"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
)

// codexAppServerApprovalOperation identifies a provider-native approval that
// is nested inside an already-running canonical Process 05 task.
const codexAppServerApprovalOperation = "CODEX_APP_SERVER_NATIVE"

// CodexAppServerApprovalBridge is the only app-layer object allowed to turn a
// native Codex approval request into a canonical MARSHAL approval record. It
// deliberately contains no accept boolean: the TUI decides the durable record
// first, then AppServer.ResolveApproval consumes it before replying to Codex.
type CodexAppServerApprovalBridge struct {
	manager *execution.ApprovalManager
	run     execution.ExecutionRun
	taskID  string
	now     func() time.Time
}

// CodexAppServerApprovals binds native tool approvals to one exact active
// Process 05 run and task. A caller cannot use it to create free-floating or
// cross-run approvals.
func (s *ExecutionService) CodexAppServerApprovals(ctx context.Context, runID, taskID string) (*CodexAppServerApprovalBridge, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	run, err := s.engine.GetRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run.State.IsTerminal() {
		return nil, fmt.Errorf("%w: native Codex approval cannot be attached to terminal run %s", model.ErrConflict, runID)
	}
	if _, ok := run.Tasks[taskID]; !ok || strings.TrimSpace(taskID) == "" {
		return nil, fmt.Errorf("%w: task %q is not part of Process 05 run %s", model.ErrInvalid, taskID, runID)
	}
	return &CodexAppServerApprovalBridge{manager: s.engine.ApprovalManager(), run: run, taskID: taskID, now: s.now}, nil
}

// Request records one exact native request. Repeated delivery of the same
// request is idempotent and returns the existing approval rather than
// presenting the operator with duplicate prompts.
func (b *CodexAppServerApprovalBridge) Request(ctx context.Context, request codex.AppServerApprovalRequest) (*execution.RuntimeApproval, error) {
	if err := b.validate(request); err != nil {
		return nil, err
	}
	scope := codexAppServerApprovalScope(request)
	records, err := b.manager.ListApprovals()
	if err != nil {
		return nil, err
	}
	for i := range records {
		record := records[i]
		if record.RunID == b.run.RunID && record.TaskID == b.taskID && record.OperationType == codexAppServerApprovalOperation && record.Scope == scope {
			return &record, nil
		}
	}
	return b.manager.RequestApproval(execution.ApprovalRequest{
		RunID: b.run.RunID, TaskID: b.taskID, PlanID: b.run.PlanID, PlanVersion: b.run.PlanVersion,
		OperationType: codexAppServerApprovalOperation, TargetResource: request.Worktree,
		RiskLevel: model.R2, Scope: scope, DiffPreview: request.Method,
		// The native digest is a reference, never a raw command payload; this
		// prevents credentials in a shell line from reaching the approval store.
		Parameters:   request.Digest,
		CurrentState: codexAppServerApprovalState(request), Now: b.now(),
	})
}

// ApproveAppServerRequest implements codex.AppServerApprovalAuthority. It
// finds the exact durable record and performs the manager's expiry, action,
// state, and one-shot-consumption checks. This method never makes a decision.
func (b *CodexAppServerApprovalBridge) ApproveAppServerRequest(ctx context.Context, request codex.AppServerApprovalRequest) error {
	if err := b.validate(request); err != nil {
		return err
	}
	records, err := b.manager.ListApprovals()
	if err != nil {
		return err
	}
	for i := range records {
		record := records[i]
		if record.RunID != b.run.RunID || record.TaskID != b.taskID || record.OperationType != codexAppServerApprovalOperation || record.Scope != codexAppServerApprovalScope(request) {
			continue
		}
		action := execution.ComputeActionDigest(record.OperationType, request.Worktree, request.Method, request.Digest)
		state := execution.ComputeStateDigest(codexAppServerApprovalState(request))
		return b.manager.ValidateAndConsume(record.ApprovalID, action, state, b.now())
	}
	return fmt.Errorf("%w: no canonical approval exists for this native Codex request", execution.ErrApprovalRequired)
}

func (b *CodexAppServerApprovalBridge) validate(request codex.AppServerApprovalRequest) error {
	if b == nil || b.manager == nil || b.run.RunID == "" || b.taskID == "" {
		return fmt.Errorf("%w: canonical Codex approval bridge is unavailable", model.ErrUnavailable)
	}
	if request.RequestID == "" || request.Method == "" || request.ThreadID == "" || request.TurnID == "" || request.ItemID == "" || request.Worktree == "" || request.Digest == "" {
		return fmt.Errorf("%w: incomplete native Codex approval binding", model.ErrInvalid)
	}
	return nil
}

func codexAppServerApprovalScope(request codex.AppServerApprovalRequest) string {
	return strings.Join([]string{
		"thread=" + request.ThreadID,
		"turn=" + request.TurnID,
		"item=" + request.ItemID,
		"method=" + request.Method,
		"digest=" + request.Digest,
	}, " ")
}

func codexAppServerApprovalState(request codex.AppServerApprovalRequest) string {
	return strings.Join([]string{
		"protocol=v2",
		"thread=" + request.ThreadID,
		"turn=" + request.TurnID,
		"item=" + request.ItemID,
		"worktree=" + request.Worktree,
		"digest=" + request.Digest,
	}, "\n")
}
