//go:build !governed

package app

import (
	"context"
	"errors"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/store"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

// NativePlanningMaximumBytes is the packet maximum: a native Marshal reads
// its complete planning context, never a refusal below the packet bound.
const NativePlanningMaximumBytes = projectcontext.MaximumPacketBytes

type NativePlanningInspection struct {
	ProjectPlanningInspection
	Bootstrap bool `json:"bootstrap"`
}

// NativePlanningRead is an in-process read proof, not an execution grant.
// No body or serialized authority is retained. TUI additionally pins its exact
// authenticated native launch. Only the complete trusted reader creates it.
type NativePlanningRead struct {
	fence                                     *store.NativePlanningFence
	mu                                        sync.Mutex
	runtime                                   *Runtime
	principal                                 authz.Principal
	session, goalID, goalDigest, packetDigest string
	goalRevision                              int64
	maxBytes                                  int
	consumed                                  bool
}

func (r *Runtime) PrepareNativePlanningRead(ctx context.Context, principal authz.Principal, session string, maxBytes int) (NativePlanningInspection, *NativePlanningRead, error) {
	var zero NativePlanningInspection
	if maxBytes < 1 || maxBytes > NativePlanningMaximumBytes || session == "" {
		return zero, nil, projectcontext.ErrBudget
	}
	if err := r.authorizeProjectContractInspection(ctx, principal); err != nil {
		return zero, nil, err
	}
	goal, err := r.store.GetActiveGoalContract(ctx, session)
	if errors.Is(err, model.ErrGoalNotFound) {
		return nativePlanningBootstrap(), nil, nil
	}
	if err != nil {
		return zero, nil, err
	}
	if !goal.Confirmation.Settled() {
		return nativePlanningBootstrap(), nil, nil
	}
	if _, err = r.store.ReadAcceptedProjectContract(ctx, r.ProjectContractBinding()); errors.Is(err, projectcontext.ErrReviewPending) {
		return nativePlanningBootstrap(), nil, nil
	} else if err != nil {
		return zero, nil, err
	}
	view, err := r.InspectProjectPlanningContext(ctx, principal, session, maxBytes)
	if err != nil {
		return zero, nil, err
	}
	after, err := r.store.GetActiveGoalContract(ctx, session)
	digest := projectcontext.Digest("marshal.native.planning.goal.v1", goal)
	if err != nil || projectcontext.Digest("marshal.native.planning.goal.v1", after) != digest || view.GoalID != goal.ID || view.GoalRevision != goal.Revision {
		return zero, nil, projectcontext.Changed("project_context_native_planning_extreme.go:74")
	}
	head, err := runtimeProofGit(ctx, r.ProjectRoot(), "rev-parse", "HEAD")
	if err != nil {
		return zero, nil, err
	}
	tree, err := execution.WorkspaceTreeDigest(r.ProjectRoot())
	if err != nil {
		return zero, nil, err
	}
	audience, err := r.projectContractOwnerAudience(ctx, principal.ID)
	if err != nil {
		return zero, nil, err
	}
	binding := projectcontext.Binding{FormatVersion: projectcontext.FormatVersion, Project: r.ProjectContractBinding(), Principal: principal.ID, Session: session, GoalID: goal.ID, GoalRevision: goal.Revision, Base: strings.TrimSpace(head), Tree: tree}
	owner, ok := auth.LocalFromContext(ctx)
	if !ok {
		return zero, nil, authz.ErrDenied
	}
	authorityRequests := []store.ProjectContextAuthorityRequest{}
	for _, actor := range []string{principal.ID, owner.ID()} {
		if len(authorityRequests) > 0 && authorityRequests[0].Binding.Principal == actor {
			continue
		}
		bound := binding
		bound.Principal = actor
		request := store.ProjectContextAuthorityRequest{Binding: bound, Purpose: store.ProjectContextAuthorityPlanning}
		if r.policyConfigured {
			request.RuntimePolicyID = string(r.runtimePolicy.PolicyID)
			request.RuntimePolicyVersion = int64(r.runtimePolicy.PolicyVersion)
		}
		authorityRequests = append(authorityRequests, request)
	}
	snapshot, fence, err := r.store.PrepareNativePlanningFence(ctx, store.ProjectPlanningContextRequest{Binding: binding, Audience: audience, ByteCap: maxBytes, Now: time.Now().UTC()}, authorityRequests)
	if err != nil {
		return zero, nil, err
	}
	if snapshot.Packet.Digest != view.PacketDigest || projectcontext.Digest("marshal.native.planning.goal.v1", snapshot.Goal) != digest {
		return zero, nil, projectcontext.Changed("project_context_native_planning_extreme.go:112")
	}
	// Detach caller-owned authority slices; later mutation cannot alter this read.
	principal.Role.Capabilities = append([]string(nil), principal.Role.Capabilities...)
	principal.Role.Authorities = append([]authz.Authority(nil), principal.Role.Authorities...)
	token := &NativePlanningRead{runtime: r, fence: fence, principal: principal, session: session, goalID: goal.ID, goalRevision: goal.Revision, goalDigest: digest, packetDigest: view.PacketDigest, maxBytes: maxBytes}
	return NativePlanningInspection{ProjectPlanningInspection: view}, token, nil
}

func nativePlanningBootstrap() NativePlanningInspection {
	return NativePlanningInspection{ProjectPlanningInspection: ProjectPlanningInspection{Context: "INTAKE ONLY: current settled Goal and reviewed accepted project context are unavailable. Collect intake and propose reviewable data only. Planning, draft admission and execution are blocked until the complete current context is read."}, Bootstrap: true}
}

// WithNativePlanningRead rechecks every required body, state/ACL/freshness,
// current full Goal and source binding before one canonical planning effect.
// Failure spends no effect; successful validation spends the token even if the
// effect itself refuses. A new complete read is required for another proposal.
func (r *Runtime) WithNativePlanningRead(ctx context.Context, token *NativePlanningRead, goalID string, goalRevision int64, effect func(context.Context) error) error {
	if token == nil || effect == nil {
		return projectcontext.ErrReviewPending
	}
	token.mu.Lock()
	defer token.mu.Unlock()
	if token.runtime != r || token.consumed || token.goalID != goalID || token.goalRevision != goalRevision {
		return projectcontext.Changed("project_context_native_planning_extreme.go:136")
	}
	view, current, err := r.PrepareNativePlanningRead(ctx, token.principal, token.session, token.maxBytes)
	if err != nil {
		return err
	}
	if current == nil || view.Bootstrap || current.goalDigest != token.goalDigest || view.PacketDigest != token.packetDigest || current.fence.AuthorityDigest() != token.fence.AuthorityDigest() {
		return projectcontext.Changed("project_context_native_planning_extreme.go:143")
	}
	fenced, err := r.store.NativePlanningFenceContext(ctx, current.fence)
	if err != nil {
		return err
	}
	token.consumed = true
	return effect(fenced)
}

// Native import has a canonical Goal ID, so mode rules must use its actual
// objective rather than treating that opaque ID as operator instructions.
func ValidateNativePlanningModes(objective string, tasks []marshal.Task) error {
	if requestsGovernedWork(objective) && !allowsNativeException(objective) {
		for _, task := range tasks {
			if task.Mode == marshal.Native {
				return projectcontext.Changed("project_context_native_planning_extreme.go:159")
			}
		}
	}
	return nil
}
