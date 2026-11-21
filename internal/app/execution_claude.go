package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/policy"
	"github.com/Zen1th53/marshal/internal/risk"
)

// runtimeClaudeHarness is the only Process 05 Claude harness registered by an
// application Runtime. It delegates to the real provider adapter; it does not
// recreate Claude execution or manufacture a successful result.
func runtimeClaudeHarness(runtime *Runtime) execution.WorkerHarness {
	return execution.NewClaudeNativeHarnessWithExecutor("", func(ctx context.Context, task execution.TaskExecution, pkg execution.ConstraintPackage, worktree string) (execution.TaskResult, error) {
		return runtime.executeProcess05Claude(ctx, task, pkg, worktree)
	})
}

func (r *Runtime) executeProcess05Claude(ctx context.Context, task execution.TaskExecution, pkg execution.ConstraintPackage, worktree string) (finalResult execution.TaskResult, finalErr error) {
	if r == nil || r.store == nil {
		return execution.TaskResult{TaskID: task.TaskID}, fmt.Errorf("%w: runtime is unavailable", model.ErrUnavailable)
	}
	if err := execution.VerifyConstraintPackage(pkg); err != nil {
		return execution.TaskResult{TaskID: task.TaskID}, err
	}
	if task.CanonicalTaskID == "" {
		return execution.TaskResult{TaskID: task.TaskID}, fmt.Errorf("%w: Process 05 Claude task %q has no canonical Runtime task binding", model.ErrConflict, task.TaskID)
	}
	if task.NativeTurn != nil {
		return execution.TaskResult{TaskID: task.TaskID, NativeTurn: task.NativeTurn}, fmt.Errorf("%w: governed Claude execution cannot continue a native turn; inspect recovery before retrying", model.ErrUnavailable)
	}
	var settle func() error
	var admissionErr error
	ctx, settle, admissionErr = r.superviseTask(ctx, task.CanonicalTaskID)
	if admissionErr != nil {
		return execution.TaskResult{TaskID: task.TaskID}, admissionErr
	}
	defer func() {
		if err := settle(); err != nil && finalErr == nil {
			finalErr = err
		}
	}()
	// The engine took the task's lease before this call; keep it while the
	// worker is prepared, then from the worker process's heartbeats.
	ctx, workerLease := r.holdWorkerLease(ctx, task)
	defer func() { finalErr = workerLease.outcome(finalErr) }()
	canonicalTask, err := r.store.GetTask(ctx, task.CanonicalTaskID)
	if err != nil {
		return execution.TaskResult{TaskID: task.TaskID}, fmt.Errorf("get canonical Process 05 task: %w", err)
	}
	agentID, err := r.process05ClaudeAgent(ctx, task)
	if err != nil {
		return execution.TaskResult{TaskID: task.TaskID}, err
	}
	if _, err := r.AssessTool(ctx, risk.AssessmentRequest{
		ID:         process05RiskAssessmentID("claude", canonicalTask.ID, task),
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
	egressRunID, err := model.NewID("RUN-EGRESS-")
	if err != nil {
		return execution.TaskResult{TaskID: task.TaskID}, err
	}
	if err := r.preflightRequiredProcess05Context(ctx, task, agentID, "claude", modelName, pkg); err != nil {
		return execution.TaskResult{TaskID: task.TaskID}, err
	}
	ctx, transportPause, err := r.reviewProviderTransport(ctx, task, pkg, canonicalTask, agentID, "claude", modelName, worktree)
	if err != nil {
		return execution.TaskResult{TaskID: task.TaskID}, err
	}
	if transportPause != nil {
		return *transportPause, nil
	}
	proxySocket, closeEgress, err := r.startProviderEgress(ctx, egressRunID, task.RunID, "claude", modelName, canonicalTask, agentID, task.RunID, model.RoleDeveloper)
	if err != nil {
		return execution.TaskResult{TaskID: task.TaskID}, err
	}
	defer closeEgress()
	provider, grantID, err := r.resolveAdapter(withProcess05OutsideReadHints(ctx, task, pkg), "claude", canonicalTask, worktree, agentID, proxySocket != "", modelName, proxySocket)
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
		return execution.TaskResult{TaskID: task.TaskID}, r.workerProviderError(ctx, err)
	}
	trustedHeader, err := r.providerConstraintPrompt(ctx, task, pkg, worktree)
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
		TrustedContext:    trustedHeader,
	}
	// The prepared adapter retains the isolation and capability runner for
	// every provider invocation, including probes and task execution.
	started := time.Now().UTC()
	workerCtx, closeKnowledge, err := r.workerMCPContext(ctx, canonicalTask.ID, agentID, "claude", &task)
	if err != nil {
		return execution.TaskResult{TaskID: task.TaskID}, err
	}
	defer closeKnowledge()
	providerCtx, finishContext, err := r.prepareWatchedRequiredProcess05Request(workerCtx, task, agentID, "claude", modelName, &request, r.workerKnowledgeContext(workerCtx, agentID, canonicalTask.ID, task.BaseCommit))
	if err != nil {
		return execution.TaskResult{TaskID: task.TaskID}, err
	}
	defer finishContext()
	providerCtx, leaseDone := r.keepWorkerLeaseAlive(providerCtx, task, &request)
	result, runErr := provider.Run(providerCtx, request)
	runErr = withWorkerLeaseOutcome(runErr, leaseDone())
	// A worker that hits its provider's own quota or rate limit marks the
	// provider limited, so the next draft or review moves off it.
	if runErr != nil || result.ExitCode != 0 {
		r.noteProviderLimit("claude", fmt.Sprint(runErr)+"\n"+string(result.Stdout)+"\n"+string(result.Stderr))
	}
	if contextErr := finishContext(); contextErr != nil {
		return execution.TaskResult{TaskID: task.TaskID, ErrorMessage: "required project context changed; provider result quarantined"}, contextErr
	}
	stdout, stdoutErr := r.sanitizeProviderOutput(ctx, result.Stdout)
	stderr, stderrErr := r.sanitizeProviderOutput(ctx, result.Stderr)
	if stdoutErr != nil || stderrErr != nil {
		return execution.TaskResult{TaskID: task.TaskID, ErrorMessage: "provider output rejected by evidence sanitizer"}, fmt.Errorf("sanitize Claude Process 05 output: stdout=%v stderr=%v", stdoutErr, stderrErr)
	}
	if runErr != nil {
		failure := workerFailureMessage(runErr.Error(), stderr, stdout)
		return execution.TaskResult{TaskID: task.TaskID, ErrorMessage: failure}, fmt.Errorf("%w: %s", runErr, failure)
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
			ProviderObservation: process05ProviderObservation(task, result, nil, "adapter_process"),
			EvidenceID:          evidenceID, TaskID: task.TaskID, ToolName: "claude", Cwd: worktree,
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
				return workerFailureMessage("Claude execution cancelled", stderr, stdout)
			}
			if result.TimedOut {
				return workerFailureMessage("Claude execution timed out", stderr, stdout)
			}
			if result.OutputTruncated {
				return workerFailureMessage("Claude output was truncated", stderr, stdout)
			}
			return workerFailureMessage("Claude execution did not complete successfully", stderr, stdout)
		}(),
	}, nil
}

// process05ClaudeAgent resolves a planned role to a real locally registered
// principal before any provider process is started. A role label is not an
// authority identity; treating it as one would bypass the canonical policy and
// capability binding.
func (r *Runtime) process05ClaudeAgent(ctx context.Context, task execution.TaskExecution) (string, error) {
	return r.resolveProcess05Worker(ctx, task, "claude", "Claude", func(p string) bool { return p == "claude" }, false)
}
