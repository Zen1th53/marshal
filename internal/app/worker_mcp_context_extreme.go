//go:build !governed && unix

package app

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/policy"
	"github.com/Zen1th53/marshal/internal/projectknowledge"
	"github.com/Zen1th53/marshal/internal/sessionmcp"
)

func (r *Runtime) workerMCPContext(ctx context.Context, taskID, agentID, provider string, attempt *execution.TaskExecution) (context.Context, func(), error) {
	noop := func() {}
	if r.workerMCPFactory == nil {
		// Embedders that have not supplied a host server keep the explicit
		// legacy read-only transport. It never authenticates as this agent.
		return r.leaseWorkerMCPContext(ctx, taskID, attempt)
	}
	root, err := filepath.EvalSymlinks(r.ProjectRoot())
	if err != nil {
		return nil, noop, err
	}
	binding, supervised := ctx.Value(taskSupervisorKey{}).(taskSupervisorBinding)
	if !supervised || binding.runtime != r || binding.id != taskID || agentID == "" {
		return nil, noop, fmt.Errorf("%w: worker MCP requires its admitting supervisor", model.ErrPolicyDenied)
	}
	var expected execution.TaskExecution
	expectedAgent, err := r.store.GetAgent(ctx, agentID)
	if err != nil || expectedAgent.ProjectID != r.ProjectID() {
		return nil, noop, model.ErrPolicyDenied
	}
	if attempt != nil {
		expected = *attempt
		expected.TargetFiles = append([]string(nil), attempt.TargetFiles...)
	}
	var expectedLease model.ActiveLease
	if attempt == nil {
		expectedLease, err = r.store.ActiveLease(ctx, taskID)
		if err != nil || expectedLease.AgentID != agentID {
			return nil, noop, model.ErrPolicyDenied
		}
	}
	validate := func(callCtx context.Context) error {
		if ctx.Err() != nil || callCtx.Err() != nil {
			return model.ErrPolicyDenied
		}
		actual, err := filepath.EvalSymlinks(r.ProjectRoot())
		if err != nil || actual != root {
			return model.ErrPolicyDenied
		}
		r.taskMu.Lock()
		live := r.taskRuns[taskID] != nil
		r.taskMu.Unlock()
		if !live {
			return model.ErrPolicyDenied
		}
		task, err := r.store.GetTask(callCtx, taskID)
		if err != nil || task.ControlState != "" || task.Status == model.TaskCancelled || task.Status == model.TaskMerged || task.Status == model.TaskSuperseded {
			return model.ErrPolicyDenied
		}
		if task.OwnerAgentID != nil && *task.OwnerAgentID != agentID {
			return model.ErrPolicyDenied
		}
		agent, err := r.store.GetAgent(callCtx, agentID)
		if err != nil || agent.ProjectID != r.ProjectID() || agent.Role != expectedAgent.Role || agent.Status == model.AgentDisabled || normalizeProvider(agent.ModelProvider) != normalizeProvider(provider) {
			return model.ErrPolicyDenied
		}
		if attempt == nil {
			lease, err := r.store.ActiveLease(callCtx, taskID)
			if err != nil || lease.Lease.ID != expectedLease.Lease.ID || lease.AgentID != agentID || lease.Lease.SessionID != expectedLease.Lease.SessionID || !lease.Lease.ExpiresAt.After(time.Now().UTC()) {
				return model.ErrPolicyDenied
			}
		} else {
			engine := r.Execution().Engine()
			run, err := engine.GetRun(callCtx, expected.RunID)
			if err != nil || (run.State != execution.RunRunning && run.State != execution.RunNeedsApproval) || engine.ValidateRunGoal(callCtx, run) != nil {
				return model.ErrPolicyDenied
			}
			runRoot, err := filepath.EvalSymlinks(run.Provenance.RepoRoot)
			if err != nil || runRoot != root {
				return model.ErrPolicyDenied
			}
			current, ok := run.Tasks[expected.TaskID]
			if !ok || current.RunID != expected.RunID || current.CanonicalTaskID != taskID || current.Attempts != expected.Attempts || current.BaseCommit != expected.BaseCommit || current.WorktreePath != expected.WorktreePath || current.AssignedHarness != expected.AssignedHarness || current.AssignedRole != expected.AssignedRole || current.AssignedModel != expected.AssignedModel || current.LeaseID != expected.LeaseID || !slices.Equal(current.TargetFiles, expected.TargetFiles) {
				return model.ErrPolicyDenied
			}
			linked, err := r.store.TaskExecutionBinding(callCtx, taskID)
			if err != nil || linked.GoalID != run.GoalID || linked.GoalRevision != run.GoalRevision || linked.PlanID != run.PlanID || linked.PlanVersion != run.PlanVersion {
				return model.ErrPolicyDenied
			}
			activePlan, err := r.store.GetActivePlan(callCtx, run.ProjectID)
			if err != nil || activePlan.ID != run.PlanID || activePlan.Version != run.PlanVersion {
				return model.ErrPolicyDenied
			}
			if current.State != execution.TaskRunning && current.State != execution.TaskWaitingTool && current.State != execution.TaskNeedsApproval {
				return model.ErrPolicyDenied
			}
			var resolved string
			switch normalizeProvider(provider) {
			case "codex":
				resolved, err = r.process05CodexAgent(callCtx, current)
			case "claude":
				resolved, err = r.process05ClaudeAgent(callCtx, current)
			case "opencode":
				resolved, err = r.process05OpenCodeAgent(callCtx, current)
			case "agy":
				if !privateAGYEnabled() {
					return model.ErrPolicyDenied
				}
				resolved, err = r.process05AntigravityAgent(callCtx, current)
			default:
				return model.ErrPolicyDenied
			}
			if err != nil || resolved != agentID || engine.ValidateWorkerAttempt(callCtx, run, current) != nil {
				return model.ErrPolicyDenied
			}
		}
		return nil
	}
	if err := validate(ctx); err != nil {
		return nil, noop, err
	}
	worktree := expected.WorktreePath
	if attempt == nil {
		task, err := r.store.GetTask(ctx, taskID)
		if err != nil || task.Worktree == nil {
			return nil, noop, model.ErrPolicyDenied
		}
		worktree = *task.Worktree
	}
	proof := workerSourceReadProof{runtime: r, task: taskID, agent: agentID, provider: provider, root: root, worktree: worktree, validate: validate, base: expected.BaseCommit, paths: expected.TargetFiles, evidence: codeEvidenceFrom(ctx)}
	if err := r.authorizeRuntime(context.WithValue(ctx, workerSourceReadKey{}, proof), agentID, taskID, provider, policy.Action("filesystem.read"), policy.Resource(root)); err != nil {
		return nil, noop, err
	}
	index, err := projectknowledge.OpenWorktree(worktree, r.layout.Root, r.layout.RuntimeDir)
	if err != nil {
		return nil, noop, err
	}
	if err := index.Ensure(ctx); err != nil {
		return nil, noop, err
	}
	server, err := r.workerMCPFactory(sessionmcp.WorkerHostSpec{Runtime: r, Root: root, TaskID: taskID, PrincipalID: agentID, Validate: validate, RequestContext: func(callCtx context.Context) context.Context {
		return context.WithValue(callCtx, workerSourceReadKey{}, proof)
	}})
	if err != nil {
		return nil, noop, err
	}
	boundCtx, err := sessionmcp.WithBoundWorker(context.WithValue(ctx, workerSourceReadKey{}, proof), root, taskID, server)
	return boundCtx, noop, err
}

type workerSourceReadKey struct{}
type workerSourceReadProof struct {
	runtime                     *Runtime
	task, agent, provider, root string
	worktree                    string
	validate                    func(context.Context) error
	base                        string
	paths                       []string
	// evidence is the Marshal task this launch serves (code evidence scope
	// and receipt record); nil outside a Marshal governed run.
	evidence *codeEvidenceBinding
}

// Derived source facts reuse the admitted read policy, not working-memory
// grants. Only this live host context can supply the proof; tool arguments or
// an auth role label cannot create it. Callers without it retain prior policy.
func (r *Runtime) authorizeBoundWorkerSourceRead(ctx context.Context, principalID, taskID string) (bool, error) {
	proof, ok := ctx.Value(workerSourceReadKey{}).(workerSourceReadProof)
	if !ok {
		return false, nil
	}
	if proof.runtime != r || proof.task != taskID || proof.agent != principalID || proof.validate == nil {
		return false, model.ErrPolicyDenied
	}
	if err := proof.validate(ctx); err != nil {
		return false, err
	}
	if err := r.authorizeRuntime(ctx, principalID, taskID, proof.provider, policy.Action("filesystem.read"), policy.Resource(proof.root)); err != nil {
		return false, err
	}
	return true, nil
}

// The existing constitutional filesystem.read rule applies only after an
// actual admitted host binding. This does not widen preflight for other callers.
func (r *Runtime) authorizeWorkerReadPreflight(ctx context.Context, subject, task, provider string, action policy.Action, resource policy.Resource) (bool, error) {
	if action != policy.Action("filesystem.read") {
		return false, nil
	}
	proof, ok := ctx.Value(workerSourceReadKey{}).(workerSourceReadProof)
	if !ok {
		return false, nil
	}
	if proof.runtime != r || proof.agent != subject || proof.task != task || proof.provider != provider || proof.root != string(resource) || proof.validate == nil {
		return true, model.ErrPolicyDenied
	}
	if err := proof.validate(ctx); err != nil {
		return true, err
	}
	agent, err := r.store.GetAgent(ctx, subject)
	if err != nil {
		return true, model.ErrPolicyDenied
	}
	return true, policy.Enforce(r.policy, model.PolicyInput{AgentID: subject, SessionID: "supervised-" + task, Role: agent.Role, TaskID: task, Operation: model.FilesystemRead, Target: string(resource), TaskOwned: true, TargetInScope: true, Required: true}, func() error { return nil })
}

func (r *Runtime) boundWorkerKnowledgeRoot(ctx context.Context) string {
	proof, ok := ctx.Value(workerSourceReadKey{}).(workerSourceReadProof)
	if ok && proof.runtime == r {
		return proof.worktree
	}
	return ""
}
