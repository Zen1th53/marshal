//go:build !governed

package app

import (
	"context"
	"reflect"

	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/verification"
)

// Verification needs qualified full context BEFORE its decision, so this
// opaque target deliberately starts from the current durable BLOCKED session.
// It provides only Stage=review context and no architecture/completion grant.
// The caller supplies its actual runtime/check input and typed verifier schema,
// calls turn outside fences, then spends the successful-turn capture before
// creating canonical evidence. The returned cleanup must run after capture.
func (r *Runtime) bindRequiredVerifierTurnTarget(ctx context.Context, cli *MarshalCLI, session verification.Session) (func(), error) {
	principal, err := requiredArchitectureOwner(ctx, r)
	if err != nil {
		return nil, err
	}
	if cli == nil || cli.runtime != r || cli.ProjectID != r.ProjectIdentity() || cli.ConversationID != "" || cli.requiredArchitectureTarget != nil || session.ID == "" || session.State != verification.Blocked {
		return nil, projectcontext.ErrReviewPending
	}
	canonical, err := r.store.GetVerificationSession(ctx, session.ID)
	if err != nil || !reflect.DeepEqual(canonical, session) {
		return nil, projectcontext.Changed("project_context_verifier_target_extreme.go:30")
	}
	binding, err := r.Verification().BindingForRun(ctx, session.Binding.RunID)
	if err != nil || binding != session.Binding {
		return nil, projectcontext.Changed("project_context_verifier_target_extreme.go:34")
	}
	root, err := r.Execution().Engine().GetRun(ctx, binding.RunID)
	if err != nil {
		return nil, err
	}
	versions, err := requiredCompletionVersions(ctx, r.Execution().Engine(), root)
	if err != nil {
		return nil, err
	}
	p, err := r.store.GetActivePlan(ctx, root.ProjectID)
	if err != nil {
		return nil, err
	}
	accepted, err := r.store.ReadAcceptedProjectContract(ctx, r.ProjectContractBinding())
	if err != nil {
		return nil, err
	}
	ref := projectcontext.ContractRef{ID: accepted.Contract.ID, Revision: accepted.Contract.Revision, Digest: accepted.Digest}
	if pin, err := requiredApprovedPlanProjectContract(p); err != nil || pin != ref {
		return nil, projectcontext.Changed("project_context_verifier_target_extreme.go:54")
	}
	selection, err := projectcontext.PlanningSelection(accepted.Contract)
	if err != nil {
		return nil, err
	}
	workspace, head, tree, err := r.resolveRequiredCompletionSource(ctx, root)
	if err != nil {
		return nil, err
	}
	b := projectcontext.Binding{FormatVersion: projectcontext.FormatVersion, Project: r.ProjectContractBinding(), Contract: ref, GoalID: root.GoalID, GoalRevision: root.GoalRevision, PlanID: root.PlanID, PlanVersion: root.PlanVersion, ApprovalScopeDigest: p.ApprovalScopeDigest, RunID: root.RunID, RunVersion: root.Version, Principal: principal.ID, Session: root.SessionID, Base: head, Tree: tree}
	cli.Dir = workspace
	target := &requiredArchitectureTurnTarget{runtime: r, cli: cli, principal: principal, binding: b, session: session, versions: versions, provider: cli.Provider, workspace: workspace, head: head, tree: tree, admission: store.ProjectContextAdmission{Selection: selection}, verification: true}
	cli.requiredArchitectureTarget = target
	return func() { target.consumed = true; cli.requiredArchitectureTarget = nil }, nil
}
