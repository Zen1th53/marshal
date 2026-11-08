//go:build !governed

package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/netpolicy"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
)

const providerTransportOperation = "PROVIDER_TRANSPORT"

type providerTransportState struct {
	mu     sync.RWMutex
	source func(string, string, string) bool
}
type providerTransportContextKey struct{}
type providerTransportAuthorization struct {
	binding       providerTransportBinding
	approvalID    string
	action, state string
	prompt        *providerPromptAdmission
}
type providerTransportTaskApproval struct {
	Required          bool
	ID, Action, State string
	Expires           time.Time
}
type providerTransportBinding struct {
	Version                                                                                                                                                             int
	Original                                                                                                                                                            providerTransportTaskApproval
	Project, Run, Task, CanonicalTask, Plan, Base, Head, Tree, Risk, Worktree, Agent, Provider, Model, Endpoint, Profile, Package, PlanDigest, AgentRole, AssignedAgent string
	PlanVersion                                                                                                                                                         int64
	Attempt                                                                                                                                                             int
	AdditionalEndpoints                                                                                                                                                 []string `json:"AdditionalEndpoints,omitempty"`
}

// SetProviderTransportReviewSource installs only trusted workspace composition.
// It checks the current authenticated Marshal launch; it grants no transport.
func (r *Runtime) SetProviderTransportReviewSource(check func(string, string, string) bool) {
	r.providerTransport.mu.Lock()
	r.providerTransport.source = check
	r.providerTransport.mu.Unlock()
}
func (r *Runtime) transportSourceCurrent(launch, owner, provider string) bool {
	r.providerTransport.mu.RLock()
	f := r.providerTransport.source
	r.providerTransport.mu.RUnlock()
	return f != nil && f(launch, owner, provider)
}
func transportApprovalRequest(b providerTransportBinding) execution.ApprovalRequest {
	b = cloneProviderTransportBinding(b)
	body, _ := json.Marshal(b)
	return execution.ApprovalRequest{OperationType: providerTransportOperation, TargetResource: providerTransportTarget(b), RiskLevel: model.R3, Scope: "host provider execution; credential consent is separate; binding Attempt counts execution-engine admissions, including preflight/resume; Runtime task_get attempt is a separate canonical-task counter; neither counter proves prior model calls; this transport gate precedes the current adapter.Run; exact binding " + string(body), Parameters: string(body), CurrentState: string(body), TTL: 15 * time.Minute}
}

func (r *Runtime) reviewProviderTransport(ctx context.Context, task execution.TaskExecution, pkg execution.ConstraintPackage, canonical model.Task, agent, provider, modelName, worktree string) (context.Context, *execution.TaskResult, error) {
	// The canonical provider pause requires approval on retry. Bind that exact
	// next package, preserving every other constraint field and its digest.
	if err := execution.VerifyConstraintPackage(pkg); err != nil {
		return ctx, nil, err
	}
	pkg.ApprovalRequired = true
	pkg.Digest = execution.ComputeConstraintDigest(pkg)
	if canonical.Risk != model.R2 && canonical.Risk != model.R3 {
		return ctx, nil, nil
	}
	if r.adapters[provider] != nil {
		return ctx, nil, nil
	} // injected tests establish no transport authority
	run, err := r.Execution().Engine().GetRun(ctx, task.RunID)
	if err != nil {
		return ctx, nil, err
	}
	actual, ok := run.Tasks[task.TaskID]
	if !ok || actual.CanonicalTaskID != canonical.ID || actual.AssignedHarness != provider || actual.AssignedModel != modelName || actual.WorktreePath != worktree || actual.RunRevision != task.RunRevision || string(run.ProjectID) != r.ProjectIdentity() {
		return ctx, nil, execution.ErrRunConflict
	}
	if err := r.Execution().Engine().ValidateWorkerAttempt(ctx, run, actual); err != nil {
		return ctx, nil, err
	}
	if err := r.Execution().Engine().ValidateRunGoal(ctx, run); err != nil {
		return ctx, nil, err
	}
	if run.ParentRunID != "" {
		if err := r.Execution().Engine().ValidateTaskLaneBinding(ctx, run.RunID); err != nil {
			return ctx, nil, err
		}
	}
	planDigest, err := r.transportPlanDigest(ctx, run, task)
	if err != nil {
		return ctx, nil, err
	}
	head, err := gitMarshal(ctx, worktree, "rev-parse", "HEAD")
	if err != nil {
		return ctx, nil, err
	}
	tree, err := execution.WorkspaceTreeDigest(worktree)
	if err != nil {
		return ctx, nil, err
	}
	profile, err := r.providerTransportHarnessProfile(ctx, provider)
	if err != nil || profile == nil {
		return ctx, nil, fmt.Errorf("verified transport harness unavailable: %v", err)
	}
	raw, _ := json.Marshal(profile)
	sum := sha256.Sum256(raw)
	b := providerTransportBinding{Project: r.ProjectIdentity(), Run: run.RunID, Task: task.TaskID, CanonicalTask: canonical.ID, Plan: run.PlanID, PlanVersion: run.PlanVersion, Base: task.BaseCommit, Head: head, Tree: tree, Risk: string(canonical.Risk), Worktree: filepath.Clean(worktree), Agent: agent, Provider: provider, Model: modelName, Profile: hex.EncodeToString(sum[:]), Package: transportPackageDigest(pkg), PlanDigest: planDigest, AgentRole: task.AssignedRole, AssignedAgent: task.AssignedAgent}
	b.Version = 1
	b.Attempt = task.Attempts + 1
	manager := r.Execution().Engine().ApprovalManager()
	required, err := r.transportTaskApprovalRequired(ctx, run, task)
	if err != nil {
		return ctx, nil, err
	}
	b.Original.Required = required || task.ApprovalRequired
	if task.ApprovalID != "" {
		a, err := r.Execution().Engine().ApprovalManager().GetApproval(task.ApprovalID)
		if err != nil {
			return ctx, nil, err
		}
		if a.OperationType == providerTransportOperation {
			b.Attempt = task.Attempts
			var prior providerTransportBinding
			_, frozen, framed := strings.Cut(a.Scope, "exact binding ")
			if !framed || json.Unmarshal([]byte(frozen), &prior) != nil || a.Scope != transportApprovalRequest(prior).Scope || prior.Version != 1 || !validProviderTransportEndpoints(prior) || (required && !prior.Original.Required) {
				return ctx, nil, execution.ErrApprovalTOCTOUViolation
			}
			b.Original = prior.Original
		} else if a.OperationType == "TASK_EXECUTE" {
			if a.ExpiresAt == nil {
				return ctx, nil, execution.ErrApprovalTOCTOUViolation
			}
			b.Original = providerTransportTaskApproval{Required: true, ID: a.ApprovalID, Action: a.ActionDigest, State: a.StateDigest, Expires: *a.ExpiresAt}
		} else {
			return ctx, nil, execution.ErrApprovalTOCTOUViolation
		}
	}
	if err := r.validateTransportTaskApproval(b, run, task, time.Now().UTC()); err != nil {
		return ctx, nil, err
	}
	credentialCtx := ctx
	// A brokered profile without a standing grant gets one fresh task-bound
	// lease; a provider-owned host credential (no broker profile) gets none.
	if netpolicy.CredentialProfileSupported(provider, modelName) && !r.HasCredentialGrant(ctx, provider) {
		credentialCtx = withBoundCredentialScope(ctx, r, r.taskBoundCredentialScope(b))
	}
	req := transportApprovalRequest(b)
	if task.ApprovalID != "" {
		approval, err := manager.GetApproval(task.ApprovalID)
		if err != nil {
			return ctx, nil, err
		}
		if approval.OperationType == providerTransportOperation {
			if approval.RunID != b.Run || approval.TaskID != b.Task || approval.PlanID != b.Plan || approval.PlanVersion != b.PlanVersion || approval.TargetResource != req.TargetResource || approval.ActionDigest != execution.ComputeActionDigest(req.OperationType, req.TargetResource, req.DiffPreview, req.Parameters) || approval.StateDigest != execution.ComputeStateDigest(req.CurrentState) || approval.Status != execution.ApprovalApproved {
				return ctx, nil, execution.ErrApprovalTOCTOUViolation
			}
			// The Marshal-reviewed MCP decision, or the Extreme runtime proof
			// for an approved-plan chain; the launch must still be current.
			proof, err := r.transportReviewProof(ctx, approval.ApprovalID, true)
			if err != nil {
				return ctx, nil, err
			}
			var reviewed execution.RuntimeApproval
			if json.Unmarshal([]byte(proof.Request.ApprovalBinding), &reviewed) != nil || reviewed.ApprovalID != approval.ApprovalID || reviewed.OperationType != providerTransportOperation || reviewed.ActionDigest != approval.ActionDigest || reviewed.StateDigest != approval.StateDigest || reviewed.RunID != approval.RunID || reviewed.TaskID != approval.TaskID || reviewed.PlanID != approval.PlanID || reviewed.PlanVersion != approval.PlanVersion {
				return ctx, nil, execution.ErrApprovalTOCTOUViolation
			}
			// Decide the exact task lease before admission; the host launch
			// then consumes and closes that same one-use lease.
			if scope, ok := r.boundScopeFromContext(credentialCtx, provider, modelName); ok {
				if _, err := r.waitBoundCredential(credentialCtx, scope); err != nil {
					return ctx, nil, err
				}
			}
			auth := providerTransportAuthorization{binding: cloneProviderTransportBinding(b), approvalID: approval.ApprovalID, action: approval.ActionDigest, state: approval.StateDigest, prompt: &providerPromptAdmission{runtime: r, binding: cloneProviderTransportBinding(b)}}
			return withProviderTransportAuthorization(credentialCtx, auth), nil, nil
		}
	}
	result := r.Execution().Engine().ProviderTransportAdmissionPause(ctx, task, req)
	return ctx, &result, nil
}

func (r *Runtime) taskBoundCredentialScope(b providerTransportBinding) boundCredentialScope {
	return boundCredentialScope{Project: string(r.admittedProjectIdentity), StorageProject: r.ProjectID(), Provider: b.Provider, Model: b.Model, Purpose: "task", Run: b.Run, Task: b.Task, CanonicalTask: b.CanonicalTask, Plan: b.Plan, PlanVersion: b.PlanVersion, Attempt: b.Attempt, Approval: b.Original.ID}
}

func (r *Runtime) authorizeReviewedProviderTransport(ctx context.Context, id, parent, provider, modelName string, task model.Task, worker, session string, role model.Role, endpoint string) (bool, error) {
	auth, ok := ctx.Value(providerTransportContextKey{}).(providerTransportAuthorization)
	if !ok {
		return false, nil
	}
	b := cloneProviderTransportBinding(auth.binding)
	if !validProviderTransportEndpoints(b) {
		return true, execution.ErrApprovalTOCTOUViolation
	}
	if endpoint != "" || b.Project != r.ProjectIdentity() || b.Run != parent || b.Run != session || b.CanonicalTask != task.ID || b.Agent != worker || b.Provider != provider || b.Model != modelName || role != model.RoleDeveloper {
		return true, execution.ErrApprovalTOCTOUViolation
	}
	return true, nil
}
func (r *Runtime) consumeReviewedProviderTransport(ctx context.Context, endpoint, egressID string) error {
	auth, ok := ctx.Value(providerTransportContextKey{}).(providerTransportAuthorization)
	if !ok {
		return nil
	}
	b := cloneProviderTransportBinding(auth.binding)
	if !validProviderTransportEndpoints(b) {
		return execution.ErrApprovalTOCTOUViolation
	}
	req := transportApprovalRequest(b)
	if execution.ComputeActionDigest(req.OperationType, req.TargetResource, req.DiffPreview, req.Parameters) != auth.action || execution.ComputeStateDigest(req.CurrentState) != auth.state {
		return execution.ErrApprovalTOCTOUViolation
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	// The empty endpoint denotes a host worker: its reviewed authorization is
	// still consumed once, without creating a scoped proxy.
	if endpoint != "" && endpoint != b.Endpoint {
		return execution.ErrApprovalTOCTOUViolation
	}
	if netpolicy.CredentialProfileSupported(b.Provider, b.Model) {
		if lease, hasBound := r.boundCredentialForContext(ctx, b.Provider); hasBound {
			if lease == nil || lease.scope != r.taskBoundCredentialScope(b) || !r.boundCredentialApproved(ctx, lease, "", "") {
				return fmt.Errorf("bounded credential consent absent")
			}
		} else if !r.HasCredentialGrant(ctx, b.Provider) {
			return fmt.Errorf("credential consent absent")
		}
	}
	run, err := r.Execution().Engine().GetRun(ctx, b.Run)
	if err != nil {
		return err
	}
	task, ok := run.Tasks[b.Task]
	if !ok || task.AssignedRole != b.AgentRole || task.AssignedAgent != b.AssignedAgent || run.State != execution.RunRunning || task.State != execution.TaskRunning || task.ApprovalID != auth.approvalID || task.CanonicalTaskID != b.CanonicalTask || task.WorktreePath != b.Worktree || task.AssignedHarness != b.Provider || task.AssignedModel != b.Model || task.BaseCommit != b.Base || task.Attempts != b.Attempt || run.PlanID != b.Plan || run.PlanVersion != b.PlanVersion || string(run.ProjectID) != b.Project {
		return execution.ErrApprovalTOCTOUViolation
	}
	if err := r.Execution().Engine().ValidateWorkerAttempt(ctx, run, task); err != nil {
		return err
	}
	if err := r.Execution().Engine().ValidateRunGoal(ctx, run); err != nil {
		return err
	}
	planDigest, err := r.transportPlanDigest(ctx, run, task)
	if err != nil || planDigest != b.PlanDigest {
		return execution.ErrApprovalTOCTOUViolation
	}
	goal, err := r.store.GetGoalContract(ctx, run.GoalID, run.GoalRevision)
	if err != nil {
		return err
	}
	currentPlan, err := r.store.GetPlan(ctx, run.PlanID, run.PlanVersion)
	if err != nil {
		return err
	}
	currentPackage, err := execution.BuildConstraintPackage(goal, task, currentPlan)
	if err != nil {
		return err
	}
	currentPackage.ApprovalRequired = true
	currentPackage.Digest = execution.ComputeConstraintDigest(currentPackage)
	if transportPackageDigest(currentPackage) != b.Package {
		return execution.ErrApprovalTOCTOUViolation
	}
	canonical, err := r.store.GetTask(ctx, b.CanonicalTask)
	if err != nil || string(canonical.Risk) != b.Risk {
		return execution.ErrApprovalTOCTOUViolation
	}
	tree, err := execution.WorkspaceTreeDigest(b.Worktree)
	if err != nil || tree != b.Tree {
		return execution.ErrApprovalTOCTOUViolation
	}
	profile, err := r.providerTransportHarnessProfile(ctx, b.Provider)
	if err != nil || profile == nil {
		return fmt.Errorf("transport harness unavailable")
	}
	raw, _ := json.Marshal(profile)
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != b.Profile {
		return execution.ErrApprovalTOCTOUViolation
	}
	var resolved string
	switch b.Provider {
	case "codex":
		resolved, err = r.process05CodexAgent(ctx, task)
	case "claude":
		resolved, err = r.process05ClaudeAgent(ctx, task)
	case "opencode":
		resolved, err = r.process05OpenCodeAgent(ctx, task)
	case "agy":
		resolved, err = r.process05AntigravityAgent(ctx, task)
	}
	if err != nil || resolved != b.Agent {
		return execution.ErrApprovalTOCTOUViolation
	}
	agent, err := r.store.GetAgent(ctx, b.Agent)
	if err != nil || agent.ModelProvider != b.Provider || agent.Status == model.AgentDisabled {
		return execution.ErrApprovalTOCTOUViolation
	}
	proof, err := r.transportReviewProof(ctx, auth.approvalID, true)
	if err != nil {
		return fmt.Errorf("transport model review absent or revoked: %v", err)
	}
	var reviewed execution.RuntimeApproval
	if json.Unmarshal([]byte(proof.Request.ApprovalBinding), &reviewed) != nil || reviewed.ApprovalID != auth.approvalID || reviewed.Scope != req.Scope || reviewed.ActionDigest != auth.action || reviewed.StateDigest != auth.state || reviewed.RunID != b.Run || reviewed.TaskID != b.Task || reviewed.PlanID != b.Plan || reviewed.PlanVersion != b.PlanVersion {
		return execution.ErrApprovalTOCTOUViolation
	}
	head, err := gitMarshal(ctx, b.Worktree, "rev-parse", "HEAD")
	if err != nil || head != b.Head {
		return execution.ErrApprovalTOCTOUViolation
	}
	required, err := r.transportTaskApprovalRequired(ctx, run, task)
	if err != nil || b.Version != 1 || (required && !b.Original.Required) {
		return execution.ErrApprovalTOCTOUViolation
	}
	now := time.Now().UTC()
	if err := r.validateTransportTaskApproval(b, run, task, now); err != nil {
		return err
	}
	// Each record is consumed under its durable manager lock. If the second
	// consume fails, the original stays spent and no provider starts; never
	// roll back or renew authorization to make a partial pair reusable.
	if b.Original.Required {
		if err := r.Execution().Engine().ApprovalManager().ValidateAndConsume(b.Original.ID, b.Original.Action, b.Original.State, time.Now().UTC()); err != nil {
			return err
		}
	}
	if err := r.Execution().Engine().ApprovalManager().ValidateAndConsume(auth.approvalID, auth.action, auth.state, time.Now().UTC()); err != nil {
		return err
	}
	auditReq := permission.Request{Kind: "marshal-command", Object: "provider-transport " + b.Provider + " " + req.TargetResource, RunID: b.Run, TaskID: b.Task, Scope: "exact one-shot host provider launch", SourceLaunch: proof.Source.Launch, SourceOccurrence: proof.Source.ID}
	if err := r.recordProviderTransportConsumed(ctx, auditReq, auth.approvalID, egressID); err != nil {
		return err
	}
	if auth.prompt != nil {
		auth.prompt.mu.Lock()
		if auth.prompt.runtime == r && reflect.DeepEqual(auth.prompt.binding, b) {
			auth.prompt.consumed = true
			auth.prompt.egressID = egressID
			auth.prompt.launch = proof.Source.Launch
			auth.prompt.occurrence = proof.Source.ID
		}
		auth.prompt.mu.Unlock()
	}
	return nil
}
func (r *Runtime) recordProviderTransportConsumed(ctx context.Context, req permission.Request, approvalID, egressID string) error {
	return r.store.AppendEvent(ctx, nil, model.Event{ID: "transport-consumed-" + approvalID, Type: "PROVIDER_TRANSPORT_CONSUMED", ProjectID: r.ProjectID(), Timestamp: time.Now().UTC(), Data: map[string]any{"approval_id": approvalID, "run_id": req.RunID, "task_id": req.TaskID, "egress_id": egressID, "object": req.Object, "source_launch": req.SourceLaunch, "source_occurrence": req.SourceOccurrence}})
}

func (r *Runtime) transportPlanDigest(ctx context.Context, run execution.ExecutionRun, task execution.TaskExecution) (string, error) {
	p, err := r.Plans().Current(ctx, projectid.ID(r.ProjectIdentity()))
	if err != nil {
		return "", err
	}
	if p.ID != run.PlanID || p.Version != run.PlanVersion || p.State != plan.StateApproved || p.Goal.GoalID != run.GoalID || p.Goal.Revision != run.GoalRevision {
		return "", execution.ErrRunConflict
	}
	scopeFound := false
	for _, pt := range p.Tasks {
		if pt.ID == task.TaskID && pt.Title == task.Description && pt.Mutating == task.Mutates && reflect.DeepEqual(pt.Paths, task.TargetFiles) {
			scopeFound = true
		}
	}
	if !scopeFound {
		return "", execution.ErrRunConflict
	}
	found := false
	for _, a := range p.Assignments.Assignments {
		if a.Independent || normalizeProvider(a.Harness) != task.AssignedHarness || a.Provider != task.AssignedHarness || a.Model != task.AssignedModel {
			continue
		}
		for _, id := range a.Tasks {
			if id == task.TaskID {
				found = true
			}
		}
	}
	// An operator's provider switch, recorded on the lane parent within this
	// plan version, assigns the task's current provider and model.
	if !found && !r.providerSwitchAssignment(ctx, run, task) {
		return "", execution.ErrRunConflict
	}
	body, _ := json.Marshal(p)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

// The transport review freezes the complete normalized canonical package,
// including outputs/capabilities omitted by the legacy constraint digest.
func transportPackageDigest(pkg execution.ConstraintPackage) string {
	body, _ := json.Marshal(pkg)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// The current approved plan determines ordinary task approval requirements.
// A transport pause setting ApprovalRequired does not create an original grant.
func (r *Runtime) transportTaskApprovalRequired(ctx context.Context, run execution.ExecutionRun, task execution.TaskExecution) (bool, error) {
	p, err := r.Plans().Current(ctx, projectid.ID(r.ProjectIdentity()))
	if err != nil {
		return false, err
	}
	if p.ID != run.PlanID || p.Version != run.PlanVersion || p.State != plan.StateApproved {
		return false, execution.ErrRunConflict
	}
	found, required := false, false
	for _, pt := range p.Tasks {
		if pt.ID == task.TaskID {
			found = true
			required = pt.RequiresApproval
		}
	}
	if !found {
		return false, execution.ErrRunConflict
	}
	for _, a := range p.Approvals {
		if a.Task == task.TaskID {
			required = true
		}
	}
	return required, nil
}

func (r *Runtime) validateTransportTaskApproval(b providerTransportBinding, run execution.ExecutionRun, task execution.TaskExecution, now time.Time) error {
	return r.validateTransportTaskApprovalStatus(b, run, task, now, execution.ApprovalApproved)
}

func (r *Runtime) validateTransportTaskApprovalStatus(b providerTransportBinding, run execution.ExecutionRun, task execution.TaskExecution, now time.Time, status execution.ApprovalStatus) error {
	origin := b.Original
	if b.Version != 1 {
		return execution.ErrApprovalTOCTOUViolation
	}
	if !origin.Required {
		if origin.ID != "" || origin.Action != "" || origin.State != "" || !origin.Expires.IsZero() {
			return execution.ErrApprovalTOCTOUViolation
		}
		return nil
	}
	if origin.ID == "" || origin.Expires.IsZero() || !now.Before(origin.Expires) || task.NativeTurn != nil {
		return execution.ErrApprovalTOCTOUViolation
	}
	a, err := r.Execution().Engine().ApprovalManager().GetApproval(origin.ID)
	if err != nil {
		return err
	}
	action := execution.ComputeActionDigest("TASK_EXECUTE", filepath.Join(task.TargetFiles...), "", task.Description)
	state := execution.ComputeStateDigest("")
	if a.OperationType != "TASK_EXECUTE" || a.Scope != "task_mutation" || a.RiskLevel != model.R2 || a.RunID != run.RunID || a.TaskID != task.TaskID || a.PlanID != run.PlanID || a.PlanVersion != run.PlanVersion || a.TargetResource != filepath.Join(task.TargetFiles...) || a.ActionDigest != action || a.StateDigest != state || a.ActionDigest != origin.Action || a.StateDigest != origin.State || a.Status != status || a.ExpiresAt == nil || !a.ExpiresAt.Equal(origin.Expires) || !now.Before(*a.ExpiresAt) {
		return execution.ErrApprovalTOCTOUViolation
	}
	return nil
}

func (r *Runtime) providerTransportHarnessProfile(ctx context.Context, provider string) (*model.HarnessProfile, error) {
	return r.store.GetHarnessProfile(ctx, marshalHarnessName(normalizeProvider(provider)))
}

func cloneProviderTransportBinding(b providerTransportBinding) providerTransportBinding {
	b.AdditionalEndpoints = append([]string(nil), b.AdditionalEndpoints...)
	return b
}
func providerTransportTarget(b providerTransportBinding) string {
	return b.Provider
}
func validProviderTransportEndpoints(b providerTransportBinding) bool {
	return b.Endpoint == "" && len(b.AdditionalEndpoints) == 0
}
func withProviderTransportAuthorization(ctx context.Context, auth providerTransportAuthorization) context.Context {
	auth.binding = cloneProviderTransportBinding(auth.binding)
	if auth.prompt != nil {
		p := auth.prompt
		p.mu.Lock()
		auth.prompt = &providerPromptAdmission{runtime: p.runtime, binding: cloneProviderTransportBinding(p.binding), consumed: p.consumed, used: p.used, egressID: p.egressID, launch: p.launch, occurrence: p.occurrence}
		p.mu.Unlock()
	}
	return context.WithValue(ctx, providerTransportContextKey{}, auth)
}
