package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter/claude"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
)

// claudeStreamApprovalOperation identifies a provider-native permission
// request nested inside an already-running canonical Process 05 task.
const claudeStreamApprovalOperation = "CLAUDE_STREAM_NATIVE"

// ClaudeStreamApprovalBridge is the only app-layer object allowed to turn a
// native Claude permission request into a canonical MARSHAL approval record.
// It deliberately contains no accept boolean: the TUI decides the durable
// record first, then StreamClient.ResolveApproval consumes it before the tool
// call is allowed to proceed.
type ClaudeStreamApprovalBridge struct {
	manager *execution.ApprovalManager
	run     execution.ExecutionRun
	taskID  string
	now     func() time.Time
}

// ClaudeStreamApprovals binds native tool approvals to one exact active
// Process 05 run and task. A caller cannot use it to create free-floating or
// cross-run approvals.
func (s *ExecutionService) ClaudeStreamApprovals(ctx context.Context, runID, taskID string) (*ClaudeStreamApprovalBridge, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	run, err := s.engine.GetRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run.State.IsTerminal() {
		return nil, fmt.Errorf("%w: native Claude approval cannot be attached to terminal run %s", model.ErrConflict, runID)
	}
	if _, ok := run.Tasks[taskID]; !ok || strings.TrimSpace(taskID) == "" {
		return nil, fmt.Errorf("%w: task %q is not part of Process 05 run %s", model.ErrInvalid, taskID, runID)
	}
	return &ClaudeStreamApprovalBridge{manager: s.engine.ApprovalManager(), run: run, taskID: taskID, now: s.now}, nil
}

// Request records one exact native request. Repeated delivery of the same
// request is idempotent and returns the existing approval rather than
// presenting the operator with duplicate prompts.
func (b *ClaudeStreamApprovalBridge) Request(ctx context.Context, request claude.StreamApprovalRequest) (*execution.RuntimeApproval, error) {
	if err := b.validate(request); err != nil {
		return nil, err
	}
	scope := claudeStreamApprovalScope(request)
	records, err := b.manager.ListApprovals()
	if err != nil {
		return nil, err
	}
	for i := range records {
		record := records[i]
		if record.RunID == b.run.RunID && record.TaskID == b.taskID && record.OperationType == claudeStreamApprovalOperation && record.Scope == scope {
			return &record, nil
		}
	}
	return b.manager.RequestApproval(execution.ApprovalRequest{
		RunID: b.run.RunID, TaskID: b.taskID, PlanID: b.run.PlanID, PlanVersion: b.run.PlanVersion,
		OperationType: claudeStreamApprovalOperation, TargetResource: request.Worktree,
		// DiffPreview must hold the same field ApproveStreamRequest recomputes
		// the action digest over, or ValidateAndConsume can never agree with
		// the stored record and every approval fails closed as a TOCTOU
		// violation. The tool name is carried in Scope instead.
		RiskLevel: model.R2, Scope: scope, DiffPreview: request.Method,
		// The native digest is a reference, never a raw tool payload; this
		// keeps credentials in a command line out of the approval store.
		Parameters:   request.Digest,
		CurrentState: claudeStreamApprovalState(request), Now: b.now(),
	})
}

// ApproveStreamRequest implements claude.StreamApprovalAuthority. It finds the
// exact durable record and performs the manager's expiry, action, state, and
// one-shot-consumption checks. This method never makes a decision.
func (b *ClaudeStreamApprovalBridge) ApproveStreamRequest(ctx context.Context, request claude.StreamApprovalRequest) error {
	if err := b.validate(request); err != nil {
		return err
	}
	records, err := b.manager.ListApprovals()
	if err != nil {
		return err
	}
	for i := range records {
		record := records[i]
		if record.RunID != b.run.RunID || record.TaskID != b.taskID || record.OperationType != claudeStreamApprovalOperation || record.Scope != claudeStreamApprovalScope(request) {
			continue
		}
		action := execution.ComputeActionDigest(record.OperationType, request.Worktree, request.Method, request.Digest)
		state := execution.ComputeStateDigest(claudeStreamApprovalState(request))
		return b.manager.ValidateAndConsume(record.ApprovalID, action, state, b.now())
	}
	return fmt.Errorf("%w: no canonical approval exists for this native Claude request", execution.ErrApprovalRequired)
}

func (b *ClaudeStreamApprovalBridge) validate(request claude.StreamApprovalRequest) error {
	if b == nil || b.manager == nil || b.run.RunID == "" || b.taskID == "" {
		return fmt.Errorf("%w: canonical Claude approval bridge is unavailable", model.ErrUnavailable)
	}
	if request.RequestID == "" || request.Method == "" || request.SessionID == "" || request.TurnID == "" || request.ToolName == "" || request.Worktree == "" || request.Digest == "" {
		return fmt.Errorf("%w: incomplete native Claude approval binding", model.ErrInvalid)
	}
	return nil
}

func claudeStreamApprovalScope(request claude.StreamApprovalRequest) string {
	return strings.Join([]string{
		"session=" + request.SessionID,
		"turn=" + request.TurnID,
		"tool=" + request.ToolName,
		"method=" + request.Method,
		"digest=" + request.Digest,
	}, " ")
}

func claudeStreamApprovalState(request claude.StreamApprovalRequest) string {
	return strings.Join([]string{
		"protocol=stream-json/v1",
		"session=" + request.SessionID,
		"turn=" + request.TurnID,
		"tool=" + request.ToolName,
		"worktree=" + request.Worktree,
		"digest=" + request.Digest,
	}, "\n")
}
