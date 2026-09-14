package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/adapter/claude"
	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/policy"
	"github.com/Zen1th53/marshal/internal/risk"
)

// claudeStreamClient is deliberately the narrow typed lifecycle exposed to
// Process 05. It is not a generic Claude CLI client and does not expose shell,
// filesystem, plugin, MCP, or remote-control operations.
type claudeStreamClient interface {
	StartSession(context.Context, adapter.Request) (claude.StreamSession, error)
	ResumeSession(context.Context, string, adapter.Request) (claude.StreamSession, error)
	StartTurn(context.Context, claude.StreamSession, adapter.Request) (claude.StreamTurn, error)
	WaitTurn(context.Context, claude.StreamTurn) (claude.StreamTurnResult, error)
	InterruptTurn(context.Context, claude.StreamTurn) error
	DeclineApproval(context.Context, claude.StreamApprovalRequest) error
	ResolveApproval(context.Context, claude.StreamApprovalRequest, claude.StreamApprovalAuthority) error
	Close() error
}

type liveClaudeStreamTurn struct {
	client  claudeStreamClient
	binding execution.NativeTurnBinding
	// initialWorktreeDigest is retained only while the native turn is live.
	// Unlike binding.WorktreeDigest, which advances to the exact pause point
	// for approval TOCTOU checks, it proves whether a mutating task made any
	// worktree change at all before a terminal success can be recorded.
	initialWorktreeDigest string
	approval              *claude.StreamApprovalRequest
	runCtx                context.Context
	cancel                context.CancelFunc
	// accepted becomes true only after ResolveApproval has consumed the exact
	// canonical approval. Before then the stored pause digest is fail-closed.
	accepted bool
}

// runtimeClaudeHarness is the only Process 05 Claude harness registered by an
// application Runtime. It delegates to the real provider adapter; it does not
// recreate Claude execution or manufacture a successful result.
func runtimeClaudeHarness(runtime *Runtime) execution.WorkerHarness {
	return execution.NewClaudeNativeHarnessWithExecutor("", func(ctx context.Context, task execution.TaskExecution, pkg execution.ConstraintPackage, worktree string) (execution.TaskResult, error) {
		return runtime.executeProcess05Claude(ctx, task, pkg, worktree)
	})
}

func (r *Runtime) executeProcess05Claude(ctx context.Context, task execution.TaskExecution, pkg execution.ConstraintPackage, worktree string) (execution.TaskResult, error) {
	if r == nil || r.store == nil {
		return execution.TaskResult{TaskID: task.TaskID}, fmt.Errorf("%w: runtime is unavailable", model.ErrUnavailable)
	}
	if err := execution.VerifyConstraintPackage(pkg); err != nil {
		return execution.TaskResult{TaskID: task.TaskID}, err
	}
	if task.CanonicalTaskID == "" {
		return execution.TaskResult{TaskID: task.TaskID}, fmt.Errorf("%w: Process 05 Claude task %q has no canonical Runtime task binding", model.ErrConflict, task.TaskID)
	}
	canonicalTask, err := r.store.GetTask(ctx, task.CanonicalTaskID)
	if err != nil {
		return execution.TaskResult{TaskID: task.TaskID}, fmt.Errorf("get canonical Process 05 task: %w", err)
	}
	agentID, err := r.process05ClaudeAgent(ctx, task)
	if err != nil {
		return execution.TaskResult{TaskID: task.TaskID}, err
	}
	if _, err := r.AssessTool(ctx, risk.AssessmentRequest{
		ID:         risk.AssessmentID("process05-claude-" + canonicalTask.ID),
		Descriptor: risk.ToolDescriptor{Tool: "marshal-process05", Action: "shell.execute", Resource: worktree, Factors: risk.Factors{ExternalWrite: true, ScopeBreadth: 1}},
	}); err != nil {
		return execution.TaskResult{TaskID: task.TaskID}, err
	}
	if err := r.authorizeRuntime(ctx, agentID, canonicalTask.ID, "claude", policy.Action("shell.execute"), policy.Resource(worktree)); err != nil {
		return execution.TaskResult{TaskID: task.TaskID}, err
	}
	modelName := task.AssignedModel
	if modelName == "" {
		if preference, prefErr := r.ClaudeModelPreference(ctx); prefErr == nil {
			modelName = preference.Model
		}
	}
	provider, grantID, err := r.resolveAdapter(ctx, "claude", canonicalTask, worktree, agentID, false, modelName, "")
	if err != nil {
		return execution.TaskResult{TaskID: task.TaskID}, err
	}
	if grantID != "" && r.capabilityBroker != nil {
		defer func() {
			_ = r.capabilityBroker.Revoke(context.Background(), capability.RevokeRequest{GrantID: grantID, Actor: capability.SubjectID("runtime")})
		}()
	}
	probe, err := provider.Probe(ctx)
	if err != nil {
		return execution.TaskResult{TaskID: task.TaskID}, err
	}
	request := adapter.Request{
		TaskID:            canonicalTask.ID,
		Title:             pkg.TaskDescription,
		Worktree:          worktree,
		Model:             modelName,
		AllowedOperations: []string{"filesystem.read", "filesystem.write", "shell.execute"},
		EvidenceRequired:  append([]string(nil), pkg.VerificationObligations...),
		TrustedContext:    pkg.FormatPromptHeader(),
	}
	// The stream session is the preferred native lifecycle. A fallback to the
	// one-shot `claude -p` path is permitted only before a native session or
	// turn exists; after a turn starts, retrying could run the task twice.
	if cli, ok := provider.(*claude.Client); ok {
		streamResult, handled, streamErr := r.executeProcess05ClaudeStream(ctx, task, pkg, worktree, cli, request, probe.Version)
		if handled {
			return streamResult, streamErr
		}
		if streamErr != nil && !errors.Is(streamErr, model.ErrUnavailable) && !errors.Is(streamErr, model.ErrPolicyDenied) {
			return execution.TaskResult{TaskID: task.TaskID}, streamErr
		}
	}
	started := time.Now().UTC()
	result, runErr := provider.Run(ctx, request)
	if runErr != nil {
		return execution.TaskResult{TaskID: task.TaskID, ErrorMessage: runErr.Error()}, runErr
	}
	stdout, stdoutErr := r.sanitizeProviderOutput(ctx, result.Stdout)
	stderr, stderrErr := r.sanitizeProviderOutput(ctx, result.Stderr)
	if stdoutErr != nil || stderrErr != nil {
		return execution.TaskResult{TaskID: task.TaskID, ErrorMessage: "provider output rejected by evidence sanitizer"}, fmt.Errorf("sanitize Claude Process 05 output: stdout=%v stderr=%v", stdoutErr, stderrErr)
	}
	digest := sha256.Sum256(append(append([]byte(nil), stdout...), stderr...))
	evidenceID, err := model.NewID("EVID-P05-CLAUDE-")
	if err != nil {
		return execution.TaskResult{TaskID: task.TaskID}, err
	}
	duration := result.EndedAt.Sub(result.StartedAt)
	if duration <= 0 {
		duration = time.Since(started)
	}
	success := result.Status == adapter.StatusSuccess && result.ExitCode == 0 && !result.TimedOut && !result.Cancelled && !result.OutputTruncated
	return execution.TaskResult{
		TaskID:  task.TaskID,
		Success: success,
		EvidenceList: []execution.ExecutionEvidence{{
			EvidenceID: evidenceID, TaskID: task.TaskID, ToolName: "claude", Cwd: worktree,
			ExitCode: result.ExitCode, StdoutSummary: string(stdout), StderrSummary: string(stderr),
			OutputDigest: "sha256:" + hex.EncodeToString(digest[:]), BinaryVersion: probe.Version,
			Status: execution.EvidenceValid, DurationMs: duration.Milliseconds(), RelevantFiles: task.TargetFiles, CreatedAt: time.Now().UTC(),
		}},
		Duration: duration,
		ErrorMessage: func() string {
			if success {
				return ""
			}
			if result.Cancelled {
				return "Claude execution cancelled"
			}
			if result.TimedOut {
				return "Claude execution timed out"
			}
			if result.OutputTruncated {
				return "Claude output was truncated"
			}
			return "Claude execution did not complete successfully"
		}(),
	}, nil
}

// executeProcess05ClaudeStream starts or continues one exact local Claude
// turn. A live turn remains in process memory only; the redacted binding is
// returned to Engine and persisted with the run. If a process restarts before
// the turn reaches a terminal event, we refuse rather than fabricate a result
// or start another turn.
func (r *Runtime) executeProcess05ClaudeStream(ctx context.Context, task execution.TaskExecution, pkg execution.ConstraintPackage, worktree string, cli *claude.Client, request adapter.Request, binaryVersion string) (execution.TaskResult, bool, error) {
	if task.RunID == "" || task.RunRevision <= 0 || cli == nil || cli.Binary() == "" {
		return execution.TaskResult{TaskID: task.TaskID}, false, fmt.Errorf("%w: native Claude stream binding is unavailable", model.ErrUnavailable)
	}
	key := task.RunID + "\x00" + task.TaskID
	r.claudeStreamMu.Lock()
	live := r.claudeStreamTurns[key]
	r.claudeStreamMu.Unlock()

	if live == nil && task.NativeTurn != nil {
		// A persisted turn without its owning local process proves an
		// interrupted session, not a safe invitation to start a replacement.
		return execution.TaskResult{TaskID: task.TaskID, NativeTurn: task.NativeTurn}, true, fmt.Errorf("%w: Claude turn %s cannot be reattached after restart; inspect recovery before retrying", model.ErrUnavailable, task.NativeTurn.TurnID)
	}
	if live == nil {
		runCtx, runCancel := context.WithCancel(ctx)
		factory := r.claudeStreamNew
		if factory == nil {
			factory = func(binary, version string) claudeStreamClient { return claude.NewStreamClient(binary, version) }
		}
		server := factory(cli.Binary(), binaryVersion)
		if server == nil {
			runCancel()
			return execution.TaskResult{TaskID: task.TaskID}, false, fmt.Errorf("%w: Claude stream client is unavailable", model.ErrUnavailable)
		}
		session, err := server.StartSession(runCtx, request)
		if err != nil {
			runCancel()
			_ = server.Close()
			return execution.TaskResult{TaskID: task.TaskID}, false, err
		}
		turn, err := server.StartTurn(runCtx, session, request)
		if err != nil {
			runCancel()
			_ = server.Close()
			// A session may exist but no turn was accepted; never fall back in
			// this ambiguous state.
			return execution.TaskResult{TaskID: task.TaskID}, true, err
		}
		worktreeDigest, err := execution.WorkspaceTreeDigest(worktree)
		if err != nil {
			runCancel()
			_ = server.Close()
			return execution.TaskResult{TaskID: task.TaskID}, true, err
		}
		live = &liveClaudeStreamTurn{client: server, initialWorktreeDigest: worktreeDigest, binding: execution.NativeTurnBinding{
			Provider: "claude-stream", ThreadID: turn.SessionID, TurnID: turn.TurnID, SessionID: turn.SessionID,
			Worktree: worktree, WorktreeDigest: worktreeDigest, Model: session.Model, ConstraintDigest: pkg.Digest,
			RunRevision: task.RunRevision, State: "RUNNING",
		}, runCtx: runCtx, cancel: runCancel}
		r.claudeStreamMu.Lock()
		if existing := r.claudeStreamTurns[key]; existing != nil {
			r.claudeStreamMu.Unlock()
			runCancel()
			_ = server.Close()
			return execution.TaskResult{TaskID: task.TaskID}, true, fmt.Errorf("%w: duplicate Claude turn start", model.ErrConflict)
		}
		r.claudeStreamTurns[key] = live
		r.claudeStreamMu.Unlock()
		// Persist the accepted native identity before WaitTurn. Without this
		// durable binding, Control could interrupt the live provider process
		// but Process 05 would have no exact turn to cancel, leaving the task
		// in an ambiguous running state.
		if err := r.Execution().Engine().BindNativeTurn(ctx, task.RunID, task.TaskID, live.binding); err != nil {
			r.removeLiveClaudeStreamTurn(key)
			return execution.TaskResult{TaskID: task.TaskID, NativeTurn: &live.binding}, true, err
		}
	}
	if live.binding.Worktree != worktree || live.binding.ConstraintDigest != pkg.Digest || (task.NativeTurn != nil && (task.NativeTurn.ThreadID != live.binding.ThreadID || task.NativeTurn.TurnID != live.binding.TurnID || task.NativeTurn.WorktreeDigest != live.binding.WorktreeDigest)) {
		return execution.TaskResult{TaskID: task.TaskID, NativeTurn: &live.binding}, true, fmt.Errorf("%w: Claude stream binding changed before turn continuation", model.ErrConflict)
	}
	if !live.accepted {
		if digest, err := execution.WorkspaceTreeDigest(worktree); err != nil || digest != live.binding.WorktreeDigest {
			if err != nil {
				return execution.TaskResult{TaskID: task.TaskID, NativeTurn: &live.binding}, true, err
			}
			return execution.TaskResult{TaskID: task.TaskID, NativeTurn: &live.binding}, true, fmt.Errorf("%w: worktree changed while Claude native turn was paused", model.ErrConflict)
		}
	}
	live.binding.RunRevision = task.RunRevision
	turnCtx := live.runCtx
	if turnCtx == nil {
		return execution.TaskResult{TaskID: task.TaskID, NativeTurn: &live.binding}, true, fmt.Errorf("%w: native Claude turn has no termination context", model.ErrUnavailable)
	}
	result, err := live.client.WaitTurn(turnCtx, claude.StreamTurn{SessionID: live.binding.ThreadID, TurnID: live.binding.TurnID})
	if err != nil {
		var nativeApproval *claude.StreamApprovalRequiredError
		if errors.As(err, &nativeApproval) {
			boundApproval, bindErr := claude.BindStreamApprovalWorktree(nativeApproval.Request, worktree)
			if bindErr != nil {
				return execution.TaskResult{TaskID: task.TaskID, NativeTurn: &live.binding}, true, bindErr
			}
			// Claude may have completed earlier governed actions in this same
			// turn before asking permission for its next one. The pre-turn tree
			// digest is therefore not the TOCTOU boundary; snapshot the tree at
			// the exact pause instead.
			pauseDigest, digestErr := execution.WorkspaceTreeDigest(worktree)
			if digestErr != nil {
				return execution.TaskResult{TaskID: task.TaskID, NativeTurn: &live.binding}, true, digestErr
			}
			live.binding.WorktreeDigest = pauseDigest
			bridge, bridgeErr := r.Execution().ClaudeStreamApprovals(ctx, task.RunID, task.TaskID)
			if bridgeErr != nil {
				return execution.TaskResult{TaskID: task.TaskID, NativeTurn: &live.binding}, true, bridgeErr
			}
			record, bridgeErr := bridge.Request(ctx, boundApproval)
			if bridgeErr != nil {
				return execution.TaskResult{TaskID: task.TaskID, NativeTurn: &live.binding}, true, bridgeErr
			}
			live.approval = &boundApproval
			live.binding.State = "APPROVAL_REQUIRED"
			return execution.TaskResult{TaskID: task.TaskID, NeedsApproval: true, NativeApprovalID: record.ApprovalID, NativeTurn: &live.binding, ApprovalReq: &execution.ApprovalRequest{OperationType: claudeStreamApprovalOperation, TargetResource: worktree, Scope: claudeStreamApprovalScope(boundApproval), DiffPreview: boundApproval.ToolName, Parameters: boundApproval.Digest, CurrentState: claudeStreamApprovalState(boundApproval)}}, true, nil
		}
		return execution.TaskResult{TaskID: task.TaskID, NativeTurn: &live.binding}, true, err
	}
	// Keep the turn context alive while terminal output crosses the evidence
	// sanitizer; removeLiveClaudeStreamTurn cancels that context.
	defer r.removeLiveClaudeStreamTurn(key)
	if task.Mutates {
		finalWorktreeDigest, digestErr := execution.WorkspaceTreeDigest(worktree)
		if digestErr != nil {
			return execution.TaskResult{TaskID: task.TaskID, NativeTurn: &live.binding}, true, digestErr
		}
		if live.initialWorktreeDigest == "" || finalWorktreeDigest == live.initialWorktreeDigest {
			return execution.TaskResult{TaskID: task.TaskID, NativeTurn: &live.binding}, true, fmt.Errorf("%w: completed native Claude turn made no worktree change for mutating task", model.ErrConflict)
		}
	}
	stdout, sanitizeErr := r.sanitizeProviderOutput(ctx, []byte(result.FinalText))
	if sanitizeErr != nil {
		return execution.TaskResult{TaskID: task.TaskID, NativeTurn: &live.binding}, true, fmt.Errorf("sanitize Claude stream output: %w", sanitizeErr)
	}
	digest := sha256.Sum256(stdout)
	evidenceID, err := model.NewID("EVID-P05-CLAUDE-")
	if err != nil {
		return execution.TaskResult{TaskID: task.TaskID, NativeTurn: &live.binding}, true, err
	}
	success := result.Status == "completed"
	return execution.TaskResult{TaskID: task.TaskID, Success: success, NativeTurn: &live.binding, EvidenceList: []execution.ExecutionEvidence{{EvidenceID: evidenceID, TaskID: task.TaskID, ToolName: "claude-stream", Cwd: worktree, ExitCode: 0, StdoutSummary: string(stdout), OutputDigest: "sha256:" + hex.EncodeToString(digest[:]), BinaryVersion: binaryVersion, Status: execution.EvidenceValid, DurationMs: result.Usage.DurationMs, RelevantFiles: task.TargetFiles, CreatedAt: time.Now().UTC()}}, ErrorMessage: func() string {
		if success {
			return ""
		}
		if result.ErrorMessage != "" {
			return "Claude stream turn failed: " + result.ErrorMessage
		}
		return "Claude stream turn did not complete successfully"
	}()}, true, nil
}

func (r *Runtime) removeLiveClaudeStreamTurn(key string) {
	r.claudeStreamMu.Lock()
	live := r.claudeStreamTurns[key]
	delete(r.claudeStreamTurns, key)
	r.claudeStreamMu.Unlock()
	if live != nil && live.client != nil {
		if live.cancel != nil {
			live.cancel()
		}
		_ = live.client.Close()
	}
}

// process05ClaudeAgent resolves a planned role to a real locally registered
// principal before any provider process is started. A role label is not an
// authority identity; treating it as one would bypass the canonical policy and
// capability binding.
func (r *Runtime) process05ClaudeAgent(ctx context.Context, task execution.TaskExecution) (string, error) {
	if task.AssignedAgent != "" {
		if agent, err := r.store.GetAgent(ctx, task.AssignedAgent); err == nil {
			if agent.Status != model.AgentDisabled && agent.ModelProvider == "claude" {
				return task.AssignedAgent, nil
			}
			return "", fmt.Errorf("%w: assigned Process 05 agent %q is not an eligible local Claude agent", model.ErrUnavailable, task.AssignedAgent)
		}
	}
	agents, err := r.Agents(ctx)
	if err != nil {
		return "", fmt.Errorf("list Process 05 agents: %w", err)
	}
	var eligible []model.Agent
	var allClaude []model.Agent
	for _, agent := range agents {
		if agent.Status != model.AgentDisabled && agent.ModelProvider == "claude" {
			allClaude = append(allClaude, agent)
			if task.AssignedRole != "" && string(agent.Role) == task.AssignedRole {
				eligible = append(eligible, agent)
			}
		}
	}
	// Some Process 04 assignment roles are provider route labels ("claude")
	// rather than an Agent.Role enum. In that case only a *unique* eligible
	// Claude principal may be used; multiple candidates remain a hard refusal.
	if len(eligible) == 0 {
		eligible = allClaude
	}
	if len(eligible) == 1 {
		return eligible[0].ID, nil
	}
	if len(eligible) > 1 {
		return "", fmt.Errorf("%w: %d eligible local Claude agents match Process 05 assignment %q; bind AssignedAgent explicitly", model.ErrInvalid, len(eligible), task.AssignedRole)
	}
	return "", fmt.Errorf("%w: no registered local Claude agent matches Process 05 assignment %q", model.ErrUnavailable, task.AssignedRole)
}
