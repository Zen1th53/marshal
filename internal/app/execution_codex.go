package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/adapter/codex"
	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/policy"
	"github.com/Zen1th53/marshal/internal/risk"
)

// codexAppServerClient is deliberately the narrow typed lifecycle exposed to
// Process 05. It is not a generic Codex RPC client and does not expose shell,
// filesystem, plugin, daemon, websocket, or remote-control operations.
type codexAppServerClient interface {
	StartSession(context.Context, adapter.Request) (codex.AppServerSession, error)
	ResumeSession(context.Context, string, adapter.Request) (codex.AppServerSession, error)
	StartTurn(context.Context, codex.AppServerSession, adapter.Request) (codex.AppServerTurn, error)
	WaitTurn(context.Context, codex.AppServerTurn) (codex.AppServerTurnResult, error)
	InterruptTurn(context.Context, codex.AppServerTurn) error
	DeclineApproval(context.Context, codex.AppServerApprovalRequest) error
	ResolveApproval(context.Context, codex.AppServerApprovalRequest, codex.AppServerApprovalAuthority) error
	Close() error
}

type liveCodexAppServerTurn struct {
	client  codexAppServerClient
	binding execution.NativeTurnBinding
	// initialWorktreeDigest is retained only while the native stdio turn is
	// live. Unlike binding.WorktreeDigest, which advances to the exact pause
	// point for approval TOCTOU checks, it proves whether a mutating task made
	// any worktree change at all before a terminal success can be recorded.
	initialWorktreeDigest string
	approval              *codex.AppServerApprovalRequest
	runCtx                context.Context
	cancel                context.CancelFunc
	// accepted becomes true only after ResolveApproval has consumed the exact
	// canonical approval and sent native accept. Before then the stored pause
	// digest is fail-closed; after then changes are the expressly-approved
	// provider action, not an external TOCTOU mutation.
	accepted bool
}

// runtimeCodexHarness is the only Process 05 Codex harness registered by an
// application Runtime. It delegates to the existing real provider adapter;
// it does not recreate Codex execution or manufacture a successful result.
func runtimeCodexHarness(runtime *Runtime) execution.WorkerHarness {
	return execution.NewCodexNativeHarnessWithExecutor("", func(ctx context.Context, task execution.TaskExecution, pkg execution.ConstraintPackage, worktree string) (execution.TaskResult, error) {
		return runtime.executeProcess05Codex(ctx, task, pkg, worktree)
	})
}

func (r *Runtime) executeProcess05Codex(ctx context.Context, task execution.TaskExecution, pkg execution.ConstraintPackage, worktree string) (execution.TaskResult, error) {
	if r == nil || r.store == nil {
		return execution.TaskResult{TaskID: task.TaskID}, fmt.Errorf("%w: runtime is unavailable", model.ErrUnavailable)
	}
	if err := execution.VerifyConstraintPackage(pkg); err != nil {
		return execution.TaskResult{TaskID: task.TaskID}, err
	}
	if task.CanonicalTaskID == "" {
		return execution.TaskResult{TaskID: task.TaskID}, fmt.Errorf("%w: Process 05 Codex task %q has no canonical Runtime task binding", model.ErrConflict, task.TaskID)
	}
	canonicalTask, err := r.store.GetTask(ctx, task.CanonicalTaskID)
	if err != nil {
		return execution.TaskResult{TaskID: task.TaskID}, fmt.Errorf("get canonical Process 05 task: %w", err)
	}
	agentID, err := r.process05CodexAgent(ctx, task)
	if err != nil {
		return execution.TaskResult{TaskID: task.TaskID}, err
	}
	if _, err := r.AssessTool(ctx, risk.AssessmentRequest{
		ID:         risk.AssessmentID("process05-codex-" + canonicalTask.ID),
		Descriptor: risk.ToolDescriptor{Tool: "marshal-process05", Action: "shell.execute", Resource: worktree, Factors: risk.Factors{ExternalWrite: true, ScopeBreadth: 1}},
	}); err != nil {
		return execution.TaskResult{TaskID: task.TaskID}, err
	}
	if err := r.authorizeRuntime(ctx, agentID, canonicalTask.ID, "codex", policy.Action("shell.execute"), policy.Resource(worktree)); err != nil {
		return execution.TaskResult{TaskID: task.TaskID}, err
	}
	modelName := task.AssignedModel
	if modelName == "" {
		if preference, prefErr := r.CodexModelPreference(ctx); prefErr == nil {
			modelName = preference.Model
		}
	}
	provider, grantID, err := r.resolveAdapter(ctx, "codex", canonicalTask, worktree, agentID, false, modelName, "")
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
	// App-server is the preferred native lifecycle path. A fallback to
	// `codex exec` is permitted only before a native thread/turn exists; after
	// a turn starts, retrying through exec could run the governed task twice.
	if client, ok := provider.(*codex.Client); ok {
		appResult, handled, appErr := r.executeProcess05CodexAppServer(ctx, task, pkg, worktree, client, request, probe.Version)
		if handled {
			return appResult, appErr
		}
		if appErr != nil && !errors.Is(appErr, model.ErrUnavailable) && !errors.Is(appErr, model.ErrPolicyDenied) {
			return execution.TaskResult{TaskID: task.TaskID}, appErr
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
		return execution.TaskResult{TaskID: task.TaskID, ErrorMessage: "provider output rejected by evidence sanitizer"}, fmt.Errorf("sanitize Codex Process 05 output: stdout=%v stderr=%v", stdoutErr, stderrErr)
	}
	digest := sha256.Sum256(append(append([]byte(nil), stdout...), stderr...))
	evidenceID, err := model.NewID("EVID-P05-CODEX-")
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
			EvidenceID: evidenceID, TaskID: task.TaskID, ToolName: "codex", Cwd: worktree,
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
				return "Codex execution cancelled"
			}
			if result.TimedOut {
				return "Codex execution timed out"
			}
			if result.OutputTruncated {
				return "Codex output was truncated"
			}
			return "Codex execution did not complete successfully"
		}(),
	}, nil
}

// executeProcess05CodexAppServer starts or continues one exact local Codex
// turn. A live turn remains in process memory only; the redacted binding is
// returned to Engine and persisted with the run. If a process restarts before
// the turn reaches a terminal notification, we refuse rather than fabricate a
// result or start another turn.
func (r *Runtime) executeProcess05CodexAppServer(ctx context.Context, task execution.TaskExecution, pkg execution.ConstraintPackage, worktree string, cli *codex.Client, request adapter.Request, binaryVersion string) (execution.TaskResult, bool, error) {
	if task.RunID == "" || task.RunRevision <= 0 || cli == nil || cli.Binary() == "" {
		return execution.TaskResult{TaskID: task.TaskID}, false, fmt.Errorf("%w: native Codex app-server binding is unavailable", model.ErrUnavailable)
	}
	key := task.RunID + "\x00" + task.TaskID
	r.codexAppServerMu.Lock()
	live := r.codexAppServerTurns[key]
	r.codexAppServerMu.Unlock()

	if live == nil && task.NativeTurn != nil {
		// A persisted turn without its owning local stdio process proves an
		// interrupted session, not a safe invitation to start a replacement.
		return execution.TaskResult{TaskID: task.TaskID, NativeTurn: task.NativeTurn}, true, fmt.Errorf("%w: Codex app-server turn %s cannot be reattached after restart; inspect recovery before retrying", model.ErrUnavailable, task.NativeTurn.TurnID)
	}
	if live == nil {
		runCtx, runCancel := context.WithCancel(ctx)
		factory := r.codexAppServerNew
		if factory == nil {
			factory = func(binary, version string) codexAppServerClient { return codex.NewAppServer(binary, version) }
		}
		server := factory(cli.Binary(), binaryVersion)
		if server == nil {
			runCancel()
			return execution.TaskResult{TaskID: task.TaskID}, false, fmt.Errorf("%w: Codex app-server client is unavailable", model.ErrUnavailable)
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
			// A thread may have been created, but no turn was accepted; never
			// fall back in this ambiguous state.
			return execution.TaskResult{TaskID: task.TaskID}, true, err
		}
		worktreeDigest, err := execution.WorkspaceTreeDigest(worktree)
		if err != nil {
			runCancel()
			_ = server.Close()
			return execution.TaskResult{TaskID: task.TaskID}, true, err
		}
		live = &liveCodexAppServerTurn{client: server, initialWorktreeDigest: worktreeDigest, binding: execution.NativeTurnBinding{
			Provider: "codex-app-server", ThreadID: turn.ThreadID, TurnID: turn.TurnID, SessionID: session.SessionID,
			Worktree: worktree, WorktreeDigest: worktreeDigest, Model: session.Model, ConstraintDigest: pkg.Digest,
			RunRevision: task.RunRevision, State: "RUNNING",
		}, runCtx: runCtx, cancel: runCancel}
		r.codexAppServerMu.Lock()
		if existing := r.codexAppServerTurns[key]; existing != nil {
			r.codexAppServerMu.Unlock()
			runCancel()
			_ = server.Close()
			return execution.TaskResult{TaskID: task.TaskID}, true, fmt.Errorf("%w: duplicate Codex app-server turn start", model.ErrConflict)
		}
		r.codexAppServerTurns[key] = live
		r.codexAppServerMu.Unlock()
		// Persist the accepted native identity before WaitTurn. Without this
		// durable binding, Control could interrupt the live provider process but
		// Process 05 would have no exact turn to cancel, leaving the task in an
		// ambiguous running state. BindNativeTurn performs the run/task CAS and
		// refuses any stale or substituted worktree/constraint identity.
		if err := r.Execution().Engine().BindNativeTurn(ctx, task.RunID, task.TaskID, live.binding); err != nil {
			r.removeLiveCodexAppServerTurn(key)
			return execution.TaskResult{TaskID: task.TaskID, NativeTurn: &live.binding}, true, err
		}
	}
	if live.binding.Worktree != worktree || live.binding.ConstraintDigest != pkg.Digest || (task.NativeTurn != nil && (task.NativeTurn.ThreadID != live.binding.ThreadID || task.NativeTurn.TurnID != live.binding.TurnID || task.NativeTurn.WorktreeDigest != live.binding.WorktreeDigest)) {
		return execution.TaskResult{TaskID: task.TaskID, NativeTurn: &live.binding}, true, fmt.Errorf("%w: Codex app-server binding changed before turn continuation", model.ErrConflict)
	}
	if !live.accepted {
		if digest, err := execution.WorkspaceTreeDigest(worktree); err != nil || digest != live.binding.WorktreeDigest {
			if err != nil {
				return execution.TaskResult{TaskID: task.TaskID, NativeTurn: &live.binding}, true, err
			}
			return execution.TaskResult{TaskID: task.TaskID, NativeTurn: &live.binding}, true, fmt.Errorf("%w: worktree changed while Codex native turn was paused", model.ErrConflict)
		}
	}
	// Engine has just admitted this exact waiting task at its current CAS
	// revision. Preserve that revision on the returned binding; the stable
	// thread/turn/worktree/constraint identity above is what prevents a
	// replacement turn from being smuggled in across the handoff.
	live.binding.RunRevision = task.RunRevision
	turnCtx := live.runCtx
	if turnCtx == nil {
		return execution.TaskResult{TaskID: task.TaskID, NativeTurn: &live.binding}, true, fmt.Errorf("%w: native Codex turn has no termination context", model.ErrUnavailable)
	}
	result, err := live.client.WaitTurn(turnCtx, codex.AppServerTurn{ThreadID: live.binding.ThreadID, TurnID: live.binding.TurnID})
	if err != nil {
		var nativeApproval *codex.AppServerApprovalRequiredError
		if errors.As(err, &nativeApproval) {
			boundApproval, bindErr := codex.BindAppServerApprovalWorktree(nativeApproval.Request, worktree)
			if bindErr != nil {
				return execution.TaskResult{TaskID: task.TaskID, NativeTurn: &live.binding}, true, bindErr
			}
			// Codex may have completed earlier governed actions in this same
			// turn before asking approval for its next action. The pre-turn tree
			// digest is therefore not the TOCTOU boundary. Snapshot the tree at
			// the exact pause instead; continuation will refuse any change after
			// the operator was shown this native request.
			pauseDigest, digestErr := execution.WorkspaceTreeDigest(worktree)
			if digestErr != nil {
				return execution.TaskResult{TaskID: task.TaskID, NativeTurn: &live.binding}, true, digestErr
			}
			live.binding.WorktreeDigest = pauseDigest
			bridge, bridgeErr := r.Execution().CodexAppServerApprovals(ctx, task.RunID, task.TaskID)
			if bridgeErr != nil {
				return execution.TaskResult{TaskID: task.TaskID, NativeTurn: &live.binding}, true, bridgeErr
			}
			record, bridgeErr := bridge.Request(ctx, boundApproval)
			if bridgeErr != nil {
				return execution.TaskResult{TaskID: task.TaskID, NativeTurn: &live.binding}, true, bridgeErr
			}
			live.approval = &boundApproval
			live.binding.State = "APPROVAL_REQUIRED"
			return execution.TaskResult{TaskID: task.TaskID, NeedsApproval: true, NativeApprovalID: record.ApprovalID, NativeTurn: &live.binding, ApprovalReq: &execution.ApprovalRequest{OperationType: codexAppServerApprovalOperation, TargetResource: worktree, Scope: codexAppServerApprovalScope(boundApproval), DiffPreview: boundApproval.Method, Parameters: boundApproval.Digest, CurrentState: codexAppServerApprovalState(boundApproval)}}, true, nil
		}
		return execution.TaskResult{TaskID: task.TaskID, NativeTurn: &live.binding}, true, err
	}
	// Keep the turn context alive while terminal output crosses the evidence
	// sanitizer. removeLiveCodexAppServerTurn cancels that context; doing it
	// first made StrictSanitizer fail closed as if safe terminal evidence were
	// secret material. Cleanup still happens on every terminal return below,
	// but only after evidence has been sanitized and projected.
	defer r.removeLiveCodexAppServerTurn(key)
	if task.Mutates {
		finalWorktreeDigest, digestErr := execution.WorkspaceTreeDigest(worktree)
		if digestErr != nil {
			return execution.TaskResult{TaskID: task.TaskID, NativeTurn: &live.binding}, true, digestErr
		}
		if live.initialWorktreeDigest == "" || finalWorktreeDigest == live.initialWorktreeDigest {
			return execution.TaskResult{TaskID: task.TaskID, NativeTurn: &live.binding}, true, fmt.Errorf("%w: completed native Codex turn made no worktree change for mutating task", model.ErrConflict)
		}
	}
	stdout, sanitizeErr := r.sanitizeProviderOutput(ctx, []byte(result.FinalText))
	if sanitizeErr != nil {
		return execution.TaskResult{TaskID: task.TaskID, NativeTurn: &live.binding}, true, fmt.Errorf("sanitize Codex app-server output: %w", sanitizeErr)
	}
	digest := sha256.Sum256(stdout)
	evidenceID, err := model.NewID("EVID-P05-CODEX-")
	if err != nil {
		return execution.TaskResult{TaskID: task.TaskID, NativeTurn: &live.binding}, true, err
	}
	success := result.Status == "completed"
	return execution.TaskResult{TaskID: task.TaskID, Success: success, NativeTurn: &live.binding, EvidenceList: []execution.ExecutionEvidence{{EvidenceID: evidenceID, TaskID: task.TaskID, ToolName: "codex-app-server", Cwd: worktree, ExitCode: 0, StdoutSummary: string(stdout), OutputDigest: "sha256:" + hex.EncodeToString(digest[:]), BinaryVersion: binaryVersion, Status: execution.EvidenceValid, DurationMs: 0, RelevantFiles: task.TargetFiles, CreatedAt: time.Now().UTC()}}, ErrorMessage: func() string {
		if success {
			return ""
		}
		if result.ErrorMessage != "" {
			return "Codex app-server turn failed: " + result.ErrorMessage
		}
		return "Codex app-server turn did not complete successfully"
	}()}, true, nil
}

func (r *Runtime) removeLiveCodexAppServerTurn(key string) {
	r.codexAppServerMu.Lock()
	live := r.codexAppServerTurns[key]
	delete(r.codexAppServerTurns, key)
	r.codexAppServerMu.Unlock()
	if live != nil && live.client != nil {
		if live.cancel != nil {
			live.cancel()
		}
		_ = live.client.Close()
	}
}

// process05CodexAgent resolves a planned role to a real locally registered
// principal before any provider process is started. A role label is not an
// authority identity; treating it as one would bypass the canonical policy
// and capability binding.
func (r *Runtime) process05CodexAgent(ctx context.Context, task execution.TaskExecution) (string, error) {
	if task.AssignedAgent != "" {
		if agent, err := r.store.GetAgent(ctx, task.AssignedAgent); err == nil {
			if agent.Status != model.AgentDisabled && agent.ModelProvider == "codex" {
				return task.AssignedAgent, nil
			}
			return "", fmt.Errorf("%w: assigned Process 05 agent %q is not an eligible local Codex agent", model.ErrUnavailable, task.AssignedAgent)
		}
	}
	agents, err := r.Agents(ctx)
	if err != nil {
		return "", fmt.Errorf("list Process 05 agents: %w", err)
	}
	var eligible []model.Agent
	var allCodex []model.Agent
	for _, agent := range agents {
		if agent.Status != model.AgentDisabled && agent.ModelProvider == "codex" {
			allCodex = append(allCodex, agent)
			if task.AssignedRole != "" && string(agent.Role) == task.AssignedRole {
				eligible = append(eligible, agent)
			}
		}
	}
	// Some Process 04 assignment roles are provider route labels ("codex")
	// rather than an Agent.Role enum. In that case only a *unique* eligible
	// Codex principal may be used; multiple candidates remain a hard refusal.
	if len(eligible) == 0 {
		eligible = allCodex
	}
	if len(eligible) == 1 {
		return eligible[0].ID, nil
	}
	if len(eligible) > 1 {
		return "", fmt.Errorf("%w: %d eligible local Codex agents match Process 05 assignment %q; bind AssignedAgent explicitly", model.ErrInvalid, len(eligible), task.AssignedRole)
	}
	return "", fmt.Errorf("%w: no registered local Codex agent matches Process 05 assignment %q", model.ErrUnavailable, task.AssignedRole)
}
