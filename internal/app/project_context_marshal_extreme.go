//go:build !governed

package app

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/store"
)

// marshalKnowledgeViaRecall: a Marshal turn whose observed project knowledge
// would push it past the required-context envelope reads that knowledge
// through MCP recall instead of being refused.
const marshalKnowledgeViaRecall = true

// marshalKnowledgePacketReserve is left for the mandatory packet when deciding
// whether observed knowledge still fits inline.
const marshalKnowledgePacketReserve = 16 << 10

// marshalKnowledgeFits reports whether a prompt carrying the observed
// knowledge, escaped as the envelope check measures it, leaves room for the
// mandatory packet.
func (m *MarshalCLI) marshalKnowledgeFits(prompt, schema string) bool {
	wire, err := json.Marshal(struct{ Provider, Prompt, Schema string }{m.Provider, marshalStructuredResultPrompt(m.Provider, prompt, schema), schema})
	return err == nil && len(wire)+marshalKnowledgePacketReserve <= requiredProviderEnvelopeBytes
}

// Account for the schema and JSON string escaping as well as the complete
// mandatory prompt. This bounds our model input envelope; it does not claim
// knowledge of a provider's hidden system prompt or exact token billing.
func (m *MarshalCLI) validateRequiredMarshalModelEnvelope(prompt, schema string) error {
	wire, err := json.Marshal(struct{ Provider, Prompt, Schema string }{m.Provider, prompt, schema})
	if err != nil {
		return err
	}
	if len(wire) > requiredProviderEnvelopeBytes {
		return projectcontext.ErrBudget
	}
	return nil
}

// Trusted wiring installs runtime, never caller JSON. Standalone private CLI
// turns cannot bypass the accepted context boundary. These model turns retain
// their existing read-only modes and convey no worker execution authority.
func (m *MarshalCLI) prepareRequiredMarshalModelTurn(ctx context.Context, prompt string) (context.Context, string, func() error, error) {
	if m != nil && m.requiredArchitectureTarget != nil {
		return m.prepareRequiredArchitectureMarshalTurn(ctx, prompt, m.requiredArchitectureTarget)
	}
	if m == nil || m.runtime == nil || m.runtime.store == nil || m.ProjectID != m.runtime.ProjectIdentity() {
		return nil, "", nil, projectcontext.ErrReviewPending
	}
	r := m.runtime
	owner, ok := auth.LocalFromContext(ctx)
	if !ok || ctx.Value(localControlKey{}) != r || owner.ProjectID() != r.ProjectIdentity() {
		return nil, "", nil, authz.ErrDenied
	}
	principal := authz.Principal{ID: owner.ID(), Role: authz.Role{Name: "marshal", Authorities: []authz.Authority{authz.AuthorityPolicyAdmin}}}
	if err := r.authorizeProjectContractInspection(ctx, principal); err != nil {
		return nil, "", nil, err
	}
	common, err := gitMarshal(ctx, m.Dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil, "", nil, err
	}
	canonicalCommon, err := gitMarshal(ctx, r.ProjectRoot(), "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil || strings.TrimSpace(common) != strings.TrimSpace(canonicalCommon) {
		return nil, "", nil, projectcontext.Changed("project_context_marshal_extreme.go:79")
	}
	p, err := r.store.GetActivePlan(ctx, projectid.ID(r.ProjectIdentity()))
	planFound := err == nil
	if err != nil && (!errors.Is(err, plan.ErrPlanNotFound) || m.requiredGoalSession == "") {
		return nil, "", nil, projectcontext.ErrReviewPending
	}
	var goal model.GoalContract
	if m.requiredGoalSession != "" {
		goal, err = r.store.GetActiveGoalContract(ctx, m.requiredGoalSession)
	} else {
		goal, err = r.store.GetGoalContract(ctx, p.Goal.GoalID, p.Goal.Revision)
	}
	if err != nil {
		return nil, "", nil, err
	}
	accepted, err := r.store.ReadAcceptedProjectContract(ctx, r.ProjectContractBinding())
	if err != nil {
		return nil, "", nil, err
	}
	selection, err := projectcontext.PlanningSelection(accepted.Contract)
	if err != nil {
		return nil, "", nil, err
	}
	head, err := runtimeProofGit(ctx, m.Dir, "rev-parse", "HEAD")
	if err != nil {
		return nil, "", nil, err
	}
	tree, err := execution.WorkspaceTreeDigest(m.Dir)
	if err != nil {
		return nil, "", nil, err
	}
	contract := projectcontext.ContractRef{ID: accepted.Contract.ID, Revision: accepted.Contract.Revision, Digest: accepted.Digest}
	b := projectcontext.Binding{FormatVersion: projectcontext.FormatVersion, Project: r.ProjectContractBinding(), Contract: contract, GoalID: goal.ID, GoalRevision: goal.Revision, Principal: principal.ID, Session: goal.SessionID, Base: strings.TrimSpace(head), Tree: tree}
	observe := func(readCtx context.Context, binding projectcontext.Binding) (store.ProjectContextAuthorityObservation, error) {
		request := store.ProjectContextAuthorityRequest{Binding: binding, Purpose: store.ProjectContextAuthorityPlanning}
		if r.policyConfigured {
			request.RuntimePolicyID, request.RuntimePolicyVersion = string(r.runtimePolicy.PolicyID), int64(r.runtimePolicy.PolicyVersion)
		}
		return r.store.ReadProjectContextAuthority(readCtx, request, time.Now().UTC())
	}
	proof := requiredProjectWorkspaceProof{Runtime: r, OwnerPlanning: true, SQLAuthority: observe, Read: func(readCtx context.Context, binding projectcontext.Binding) (requiredProjectWorkspaceState, error) {
		if binding.Project != b.Project || binding.Contract != b.Contract || binding.GoalID != b.GoalID || binding.GoalRevision != b.GoalRevision || binding.Principal != b.Principal || binding.Session != b.Session || binding.Base != b.Base || binding.Tree != b.Tree || binding.TaskID != "" || binding.CanonicalTaskID != "" || binding.PlanID != "" || binding.Provider != "" {
			return requiredProjectWorkspaceState{}, projectcontext.Changed("project_context_marshal_extreme.go:122")
		}
		if err := r.authorizeProjectContractInspection(readCtx, principal); err != nil {
			return requiredProjectWorkspaceState{}, err
		}
		currentCommon, err := gitMarshal(readCtx, m.Dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
		if err != nil || strings.TrimSpace(currentCommon) != strings.TrimSpace(canonicalCommon) {
			return requiredProjectWorkspaceState{}, projectcontext.Changed("project_context_marshal_extreme.go:129")
		}
		current, err := r.store.GetActivePlan(readCtx, projectid.ID(r.ProjectIdentity()))
		if planFound && (err != nil || !reflect.DeepEqual(current, p)) || !planFound && !errors.Is(err, plan.ErrPlanNotFound) {
			return requiredProjectWorkspaceState{}, projectcontext.Changed("project_context_marshal_extreme.go:133")
		}
		currentHead, err := runtimeProofGit(readCtx, m.Dir, "rev-parse", "HEAD")
		if err != nil {
			return requiredProjectWorkspaceState{}, err
		}
		currentTree, err := execution.WorkspaceTreeDigest(m.Dir)
		if err != nil {
			return requiredProjectWorkspaceState{}, err
		}
		authority, err := observe(readCtx, binding)
		if err != nil {
			return requiredProjectWorkspaceState{}, err
		}
		digest := projectcontext.Digest("marshal.required.owner-model.v1", struct {
			Plan      plan.ExecutionPlan
			Authority string
		}{p, authority.Digest})
		return requiredProjectWorkspaceState{Workspace: m.Dir, Base: b.Base, Head: strings.TrimSpace(currentHead), Tree: currentTree, AuthorityDigest: digest, SQLAuthorityDigest: authority.Digest}, nil
	}}
	sealed := context.WithValue(ctx, requiredProjectWorkspaceKey{}, proof)
	release, err := admitRunControlLifecycle(r)
	if err != nil {
		return nil, "", nil, err
	}
	// Local context/SQL admission alone holds the owner fence. It is released
	// before polling, process creation, user waits or any model invocation.
	token, err := r.admitRequiredProjectInvocation(sealed, principal, b, selection, prompt, requiredProviderEnvelopeBytes, "planning", "")
	var text string
	var receipt projectcontext.Receipt
	if err == nil {
		text, receipt, err = r.consumeRequiredProjectInvocation(sealed, token)
	}
	release()
	if err != nil {
		return nil, "", nil, err
	}
	owned, watch, err := r.startRequiredProjectContextWatch(ctx, receipt, token.dispatchAdmission)
	if err != nil {
		return nil, "", nil, err
	}
	m.requiredTurnPreparation = &requiredMarshalTurnPreparation{runtime: r, cli: m, receipt: receipt, admission: token.dispatchAdmission, workspace: m.Dir, head: b.Base, tree: b.Tree}
	finish := func() error {
		if err := watch.Finish(); err != nil {
			return err
		}
		// Read-only planning/review must also preserve its exact canonical plan
		// and inspected tree. This check never runs while a worker is writing.
		state, err := proof.Read(ctx, receipt.Binding)
		if err != nil {
			return err
		}
		return requiredProjectWorkspaceCurrent(ctx, receipt.Binding, state)
	}
	return owned, text, finish, nil
}

// providerLimitFallback: the private edition moves review and verification
// roles off a provider that reported its own quota or rate limit.
const providerLimitFallback = true
