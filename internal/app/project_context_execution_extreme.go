//go:build !governed

package app

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/policy"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/store"
)

// requiredExecutionContext joins the current owned engine snapshot to a live
// one-shot context admission. It performs local effects only and releases every
// owner/run/store lock before returning. The caller invokes its exact provider
// immediately afterward and remains subject to existing approvals and policy.
// This helper itself is not a provider effect or an architecture review.
func (r *Runtime) requiredExecutionContext(ctx context.Context, admitted execution.TaskExecution, agentID, provider, modelName, prefix string, byteCap int, stage string) (string, projectcontext.Receipt, error) {
	return r.requiredExecutionContextWithAdmission(ctx, admitted, agentID, provider, modelName, prefix, byteCap, stage, nil)
}

func (r *Runtime) requiredExecutionContextWithAdmission(ctx context.Context, admitted execution.TaskExecution, agentID, provider, modelName, prefix string, byteCap int, stage string, admission *store.ProjectContextAdmission) (string, projectcontext.Receipt, error) {
	var text string
	var receipt projectcontext.Receipt
	if r == nil || r.store == nil || agentID == "" || provider == "" || modelName == "" || admitted.RunID == "" || admitted.TaskID == "" {
		return text, receipt, projectcontext.ErrMissing
	}
	service := r.Execution()
	if err := service.available(); err != nil {
		return text, receipt, err
	}
	engine := service.Engine()
	release, err := admitRunControlLifecycle(r)
	if err != nil {
		return text, receipt, err
	}
	defer release()
	err = engine.WithCurrentRequiredTaskFence(ctx, admitted.RunID, func(snapshots map[string]execution.ExecutionRun) error {
		run, task, err := engine.ValidateRequiredContextFenceTask(ctx, snapshots, admitted.RunID, admitted.TaskID)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(task, admitted) || task.AssignedHarness != provider || task.AssignedModel != modelName || (task.AssignedAgent != "" && task.AssignedAgent != agentID) || string(run.ProjectID) != r.ProjectIdentity() {
			return fmt.Errorf("%w: current execution identity", projectcontext.Changed("project_context_execution_extreme.go:52"))
		}
		agent, err := r.store.GetAgent(ctx, agentID)
		if err != nil {
			return err
		}
		if agent.ProjectID != r.ProjectID() || agent.Status == model.AgentDisabled || agent.ModelProvider != provider || !agent.Role.Valid() {
			return fmt.Errorf("%w: current registered provider identity", projectcontext.Changed("project_context_execution_extreme.go:59"))
		}
		if err = r.authorizeRuntime(ctx, agentID, task.CanonicalTaskID, provider, policy.Action("shell.execute"), policy.Resource(task.WorktreePath)); err != nil {
			return err
		}
		accepted, err := r.store.ReadAcceptedProjectContract(ctx, r.ProjectContractBinding())
		if err != nil {
			return err
		}
		p, err := r.store.GetActivePlan(ctx, projectid.ID(r.ProjectIdentity()))
		if err != nil {
			return err
		}
		contractRef, err := requiredApprovedPlanProjectContract(p)
		if err != nil {
			return err
		}
		invocationPrefix, err := approvedExecutionTaskInstructions(p, run, task, prefix, byteCap)
		if err != nil {
			return err
		}
		tree, err := execution.WorkspaceTreeDigest(task.WorktreePath)
		if err != nil {
			return err
		}
		base := strings.TrimSpace(task.BaseCommit)
		if base == "" && run.Delivery != execution.DeliveryPreserveBranch {
			// Conventional engine workspaces omit Task.BaseCommit. Their actual
			// canonical baseline checkpoint pins the source before work started;
			// never invent a base from a later arbitrary HEAD.
			for _, checkpoint := range run.Checkpoints {
				if checkpoint.TaskID == "baseline" && checkpoint.RunID == run.RunID {
					if base != "" {
						return fmt.Errorf("%w: ambiguous baseline", projectcontext.Changed("project_context_execution_extreme.go:92"))
					}
					base = strings.TrimSpace(checkpoint.GitCommit)
				}
			}
		}
		if !fullProofObject(base) {
			return fmt.Errorf("%w: source baseline unavailable", projectcontext.Changed("project_context_execution_extreme.go:99"))
		}
		b := projectcontext.Binding{FormatVersion: projectcontext.FormatVersion, Project: r.ProjectContractBinding(), Contract: contractRef, GoalID: run.GoalID, GoalRevision: run.GoalRevision, PlanID: run.PlanID, PlanVersion: run.PlanVersion, ApprovalScopeDigest: p.ApprovalScopeDigest, RunID: run.RunID, RunVersion: run.Version, TaskID: task.TaskID, CanonicalTaskID: task.CanonicalTaskID, Attempt: task.Attempts, Provider: provider, Model: modelName, Principal: agentID, Session: run.SessionID, Base: base, Tree: tree}
		selection, err := requiredProjectPlanSelection(p, b, accepted.Contract)
		if err != nil {
			return err
		}
		// Validate the detached current run only; GetRun/ValidateWorkerAttempt
		// here would reenter the engine/run-store fence and can deadlock.
		observeAuthority := func(readCtx context.Context, binding projectcontext.Binding) (store.ProjectContextAuthorityObservation, error) {
			request := store.ProjectContextAuthorityRequest{Binding: binding, Purpose: store.ProjectContextAuthorityWorker}
			if r.policyConfigured {
				request.RuntimePolicyID, request.RuntimePolicyVersion = string(r.runtimePolicy.PolicyID), int64(r.runtimePolicy.PolicyVersion)
			}
			return r.store.ReadProjectContextAuthority(readCtx, request, time.Now().UTC())
		}
		proof := requiredProjectWorkspaceProof{Runtime: r, SQLAuthority: observeAuthority, Read: func(readCtx context.Context, binding projectcontext.Binding) (requiredProjectWorkspaceState, error) {
			if binding.Project != b.Project || binding.Contract != b.Contract || binding.Principal != agentID || binding.Provider != provider || binding.Model != modelName || binding.RunID != b.RunID || binding.RunVersion != b.RunVersion || binding.TaskID != b.TaskID || binding.CanonicalTaskID != b.CanonicalTaskID || binding.Attempt != b.Attempt || binding.Session != b.Session || binding.GoalID != b.GoalID || binding.GoalRevision != b.GoalRevision || binding.PlanID != b.PlanID || binding.PlanVersion != b.PlanVersion || binding.ApprovalScopeDigest != b.ApprovalScopeDigest || binding.Base != b.Base || binding.Tree != b.Tree {
				return requiredProjectWorkspaceState{}, projectcontext.Changed("project_context_execution_extreme.go:117")
			}
			_, currentTask, err := engine.ValidateRequiredContextFenceTask(readCtx, snapshots, task.RunID, task.TaskID)
			if err != nil {
				return requiredProjectWorkspaceState{}, err
			}
			if !reflect.DeepEqual(currentTask, task) {
				return requiredProjectWorkspaceState{}, projectcontext.Changed("project_context_execution_extreme.go:124")
			}
			currentAgent, err := r.store.GetAgent(readCtx, agentID)
			if err != nil || !reflect.DeepEqual(currentAgent, agent) {
				return requiredProjectWorkspaceState{}, projectcontext.Changed("project_context_execution_extreme.go:128")
			}
			if err = r.authorizeRuntime(readCtx, agentID, task.CanonicalTaskID, provider, policy.Action("shell.execute"), policy.Resource(task.WorktreePath)); err != nil {
				return requiredProjectWorkspaceState{}, err
			}
			head, err := runtimeProofGit(readCtx, task.WorktreePath, "rev-parse", "HEAD")
			if err != nil {
				return requiredProjectWorkspaceState{}, err
			}
			currentTree, err := execution.WorkspaceTreeDigest(task.WorktreePath)
			if err != nil {
				return requiredProjectWorkspaceState{}, err
			}
			authority, err := observeAuthority(readCtx, binding)
			if err != nil {
				return requiredProjectWorkspaceState{}, err
			}
			authorityDigest := projectcontext.Digest("marshal.required.owned-execution.v1", struct {
				Run          execution.ExecutionRun
				Task         execution.TaskExecution
				Agent        model.Agent
				SQLAuthority string
			}{run, task, agent, authority.Digest})
			return requiredProjectWorkspaceState{Workspace: task.WorktreePath, Base: base, Head: strings.TrimSpace(head), Tree: currentTree, AuthorityDigest: authorityDigest, SQLAuthorityDigest: authority.Digest}, nil
		}}
		sealed := context.WithValue(ctx, requiredProjectWorkspaceKey{}, proof)
		principal := authz.Principal{ID: agentID, Role: authz.Role{Name: string(agent.Role)}}
		token, err := r.admitRequiredProjectInvocation(sealed, principal, b, selection, invocationPrefix, byteCap, stage, "")
		if err != nil {
			return fmt.Errorf("required execution admission: %w", err)
		}
		text, receipt, err = r.consumeRequiredProjectInvocation(sealed, token)
		if err == nil && admission != nil {
			*admission = token.dispatchAdmission
		}
		return err
	})
	if err != nil {
		return "", projectcontext.Receipt{}, err
	}
	return text, receipt, nil
}
