//go:build !governed

package app

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/verification"
)

// An attestation in SQLite is data. This exact-runtime capability exists only
// after the actual review and atomic all-task completion transaction succeed.
type requiredContextCloseProof struct {
	mu                           sync.Mutex
	runtime                      *Runtime
	sourceObservation, principal string
	session                      verification.Session
	attestation                  verification.CompletionAttestation
	admissions                   []store.ProjectContextAdmission
	consumed                     bool
}

func (r *Runtime) retainRequiredContextCloseProof(source *requiredIntegrationSource, proof *requiredArchitectureCompletionProof, session verification.Session, attestation verification.CompletionAttestation, admissions []store.ProjectContextAdmission) error {
	r.requiredIntegrationSources.mu.Lock()
	defer r.requiredIntegrationSources.mu.Unlock()
	observed := r.requiredIntegrationSources.byRun[session.Binding.RunID]
	if observed == nil || observed.runtime != r || observed.observationID != source.observationID || source.head != proof.capture.preparation.head || source.tree != session.Binding.TreeDigest {
		return fmt.Errorf("required close retained completion source: %w", projectcontext.Changed("project_context_close_extreme.go:37"))
	}
	all := append([]store.ProjectContextAdmission{proof.capture.preparation.admission}, admissions...)
	observed.completion = &requiredContextCloseProof{runtime: r, sourceObservation: source.observationID, principal: proof.receipt.Binding.Principal, session: session, attestation: attestation, admissions: all}
	return nil
}

// requiredChecksOnlyCloseProof is the live one-shot capability a checks-only
// close presents at the close fence in place of the canonical session and
// attestation. It exists only after this runtime reobserved the same
// integration source, validated current mandatory context/freshness/authority
// admissions, and observed the approved checks all pass on its head.
type requiredChecksOnlyCloseProof struct {
	mu                           sync.Mutex
	runtime                      *Runtime
	sourceObservation, principal string
	receiptID, receiptDigest     string
	goalID                       string
	goalRevision                 int64
	admissions                   []store.ProjectContextAdmission
	consumed                     bool
}

func (r *Runtime) retainRequiredChecksOnlyCloseProof(ctx context.Context, id string, run marshal.Run, head string, receipt verification.ChecksOnlyReceipt) error {
	principal, err := requiredArchitectureOwner(ctx, r)
	if err != nil {
		return err
	}
	if r.memoryService == nil || !checksOnlyDelivery(run) || !receipt.Passed() || receipt.Head != head || receipt.RunID != id {
		return fmt.Errorf("required close checks-only receipt: %w", projectcontext.Changed("project_context_close_extreme.go:checks-only-receipt"))
	}
	r.requiredIntegrationSources.mu.Lock()
	var source *requiredIntegrationSource
	for _, candidate := range r.requiredIntegrationSources.byRun {
		if candidate.marshalRunID == id {
			if source != nil {
				r.requiredIntegrationSources.mu.Unlock()
				return fmt.Errorf("required close unique observed source: %w", projectcontext.Changed("project_context_close_extreme.go:checks-only-source"))
			}
			source = candidate
		}
	}
	r.requiredIntegrationSources.mu.Unlock()
	if source == nil || source.runtime != r || source.head != head || !reflect.DeepEqual(source.marshalRun, run) {
		return fmt.Errorf("required close checks-only source: %w", projectcontext.Changed("project_context_close_extreme.go:checks-only-source"))
	}
	p, err := r.store.GetActivePlan(ctx, source.rootRun.ProjectID)
	if err != nil || p.ID != source.rootRun.PlanID || p.Version != source.rootRun.PlanVersion || p.State != plan.StateApproved {
		return fmt.Errorf("required close checks-only plan: %w", projectcontext.Changed("project_context_close_extreme.go:checks-only-plan"))
	}
	accepted, err := r.store.ReadAcceptedProjectContract(ctx, r.ProjectContractBinding())
	if err != nil {
		return err
	}
	ref := projectcontext.ContractRef{ID: accepted.Contract.ID, Revision: accepted.Contract.Revision, Digest: accepted.Digest}
	if ref != source.contract {
		return fmt.Errorf("required close checks-only contract: %w", projectcontext.Changed("project_context_close_extreme.go:checks-only-contract"))
	}
	if pinned, err := requiredApprovedPlanProjectContract(p); err != nil || pinned != ref {
		return fmt.Errorf("required close checks-only contract pin: %w", projectcontext.Changed("project_context_close_extreme.go:checks-only-contract"))
	}
	audience, err := r.projectContractOwnerAudience(ctx, principal.ID)
	if err != nil {
		return err
	}
	goal, err := r.store.GetGoalContract(ctx, source.rootRun.GoalID, source.rootRun.GoalRevision)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	grounding := &requiredProjectGroundingBudget{Hashes: map[string]string{}}
	baseBinding := projectcontext.Binding{
		FormatVersion:       projectcontext.FormatVersion,
		Project:             r.ProjectContractBinding(),
		Contract:            ref,
		GoalID:              source.rootRun.GoalID,
		GoalRevision:        source.rootRun.GoalRevision,
		PlanID:              source.rootRun.PlanID,
		PlanVersion:         source.rootRun.PlanVersion,
		ApprovalScopeDigest: p.ApprovalScopeDigest,
		RunID:               source.rootRun.RunID,
		RunVersion:          source.rootRun.Version,
		Principal:           principal.ID,
		Session:             source.rootRun.SessionID,
		Base:                source.head,
		Tree:                source.tree,
	}
	buildAdmission := func(b projectcontext.Binding, selection projectcontext.Selection) (store.ProjectContextAdmission, error) {
		snapshot, err := r.store.ReadProjectContextSnapshotBound(ctx, b.Project, audience, selection, now, b.Contract)
		if err != nil {
			return store.ProjectContextAdmission{}, err
		}
		for _, record := range snapshot.Records {
			inputs, err := requiredProjectFreshnessInputsBounded(source.workspace, source.head, record, grounding)
			if err != nil {
				return store.ProjectContextAdmission{}, err
			}
			if r.memoryService.EvaluateFreshness(record, inputs).Classification != FreshnessFresh {
				return store.ProjectContextAdmission{}, fmt.Errorf("required close checks-only fresh memory: %w", projectcontext.Changed("project_context_close_extreme.go:checks-only-fresh"))
			}
		}
		b.RequiredStateDigest = snapshot.StateDigest
		packet, err := projectcontext.Render(projectcontext.Manifest{Contract: snapshot.Contract, Goal: projectcontext.GoalFromCanonical(goal), Binding: b, Selection: selection, Records: snapshot.Records}, projectcontext.MaximumPacketBytes)
		if err != nil {
			return store.ProjectContextAdmission{}, err
		}
		request := store.ProjectContextAuthorityRequest{Binding: b, Purpose: store.ProjectContextAuthorityCompletion}
		if r.policyConfigured {
			request.RuntimePolicyID, request.RuntimePolicyVersion = string(r.runtimePolicy.PolicyID), int64(r.runtimePolicy.PolicyVersion)
		}
		authority, err := r.store.ReadProjectContextAuthority(ctx, request, now)
		if err != nil {
			return store.ProjectContextAdmission{}, err
		}
		return store.ProjectContextAdmission{
			Stage:                "checks_only_close",
			Binding:              b,
			Audience:             audience,
			Selection:            selection,
			ExpectedStateDigest:  snapshot.StateDigest,
			ExpectedPacketDigest: packet.Digest,
			ByteCap:              projectcontext.MaximumPacketBytes,
			Now:                  now,
			ExpectedAuthority:    &authority,
		}, nil
	}
	admissions := make([]store.ProjectContextAdmission, 0, len(p.Tasks)+1)
	fullSelection, err := projectcontext.PlanningSelection(accepted.Contract)
	if err != nil {
		return err
	}
	fullAdmission, err := buildAdmission(baseBinding, fullSelection)
	if err != nil {
		return err
	}
	admissions = append(admissions, fullAdmission)
	for _, task := range p.Tasks {
		b := baseBinding
		b.TaskID, b.CanonicalTaskID = task.ID, canonicalPlanTaskID(p.ID, p.Version, task.ID)
		selection, err := requiredProjectPlanSelection(p, b, accepted.Contract)
		if err != nil {
			return err
		}
		taskAdmission, err := buildAdmission(b, selection)
		if err != nil {
			return err
		}
		admissions = append(admissions, taskAdmission)
	}
	r.requiredIntegrationSources.mu.Lock()
	defer r.requiredIntegrationSources.mu.Unlock()
	if r.requiredIntegrationSources.byRun[source.rootRun.RunID] != source {
		return fmt.Errorf("required close checks-only source changed: %w", projectcontext.Changed("project_context_close_extreme.go:checks-only-source"))
	}
	source.checksOnly = &requiredChecksOnlyCloseProof{
		runtime:           r,
		sourceObservation: source.observationID,
		principal:         principal.ID,
		receiptID:         receipt.ID,
		receiptDigest:     receipt.Digest,
		goalID:            source.rootRun.GoalID,
		goalRevision:      source.rootRun.GoalRevision,
		admissions:        admissions,
	}
	return nil
}

// The trusted common caller supplies only the final target Git effect. The
// journal's two exact writes are the sole accepted Marshal revision change.
// No model invocation or SQL transaction spans that effect.
func (r *Runtime) withRequiredContextCloseEffect(ctx context.Context, service *MarshalService, id string, baseline marshal.Run, op *marshal.LifecycleOperation, effect func() error) error {
	principal, err := requiredArchitectureOwner(ctx, r)
	if err != nil {
		return err
	}
	if service == nil || service.Store != r.store || service.Repository != r.ProjectRoot() || service.Worktrees != r.layout.Worktrees || effect == nil || op == nil || op.ID == "" || op.Kind != "close" || !op.Authorized || op.Next == nil || op.TaskID != "" || op.Before != baseline.BaseCommit {
		return fmt.Errorf("required close journal input: %w", projectcontext.Changed("project_context_close_extreme.go:53"))
	}
	r.requiredIntegrationSources.mu.Lock()
	var source *requiredIntegrationSource
	for _, candidate := range r.requiredIntegrationSources.byRun {
		if candidate.marshalRunID == id {
			if source != nil {
				r.requiredIntegrationSources.mu.Unlock()
				return fmt.Errorf("required close unique observed source: %w", projectcontext.Changed("project_context_close_extreme.go:61"))
			}
			source = candidate
		}
	}
	checksOnly := op.DeliveryKind != ""
	var proof *requiredContextCloseProof
	var checksProof *requiredChecksOnlyCloseProof
	if source != nil {
		proof, checksProof = source.completion, source.checksOnly
	}
	r.requiredIntegrationSources.mu.Unlock()
	if checksOnly {
		// A checks-only close substitutes its receipt and live capability
		// for the canonical session and attestation; every other predicate
		// below still holds.
		if op.DeliveryKind != verification.DeliveredChecksOnly || !checksOnlyDelivery(baseline) || op.Next.ChecksOnly == nil || op.Next.DeliveryKind != op.DeliveryKind || op.Next.ChecksOnly.ID != op.ReceiptID || op.Next.ChecksOnly.Digest != op.ReceiptDigest || !op.Next.ChecksOnly.Passed() {
			return fmt.Errorf("required close checks-only receipt: %w", projectcontext.Changed("project_context_close_extreme.go:checks-only-op"))
		}
		if source == nil || source.runtime != r || checksProof == nil {
			return fmt.Errorf("%w: checks-only close has no live observed source or proof in this runtime (source present: %t, proof present: %t)", projectcontext.ErrReviewPending, source != nil, checksProof != nil)
		}
		if checksProof.runtime != r || checksProof.principal != principal.ID || checksProof.sourceObservation != source.observationID || checksProof.receiptID != op.ReceiptID || checksProof.receiptDigest != op.ReceiptDigest || op.Next.ChecksOnly.Head != source.head {
			return fmt.Errorf("required close live proof owner: %w", projectcontext.Changed("project_context_close_extreme.go:checks-only-owner"))
		}
		checksProof.mu.Lock()
		defer checksProof.mu.Unlock()
		if checksProof.consumed {
			return fmt.Errorf("required close one-shot capability: %w", projectcontext.Changed("project_context_close_extreme.go:checks-only-once"))
		}
		checksProof.consumed = true
	} else {
		if source == nil || source.runtime != r || proof == nil {
			return fmt.Errorf("%w: close has no live observed source or completion proof in this runtime (source present: %t, proof present: %t)", projectcontext.ErrReviewPending, source != nil, proof != nil)
		}
		if proof.runtime != r || proof.principal != principal.ID || proof.sourceObservation != source.observationID {
			return fmt.Errorf("required close live proof owner: %w", projectcontext.Changed("project_context_close_extreme.go:75"))
		}
		proof.mu.Lock()
		defer proof.mu.Unlock()
		if proof.consumed {
			return fmt.Errorf("required close one-shot capability: %w", projectcontext.Changed("project_context_close_extreme.go:80"))
		}
		proof.consumed = true
	}
	if !reflect.DeepEqual(baseline, source.marshalRun) || op.After != source.head || op.Target != baseline.TargetRef {
		return fmt.Errorf("required close approved baseline: %w", projectcontext.Changed("project_context_close_extreme.go:84"))
	}
	next := baseline
	next.State = marshal.Closed
	if checksOnly {
		next.ChecksOnly, next.DeliveryKind = op.Next.ChecksOnly, op.DeliveryKind
	}
	if !reflect.DeepEqual(*op.Next, next) {
		return fmt.Errorf("required close authorized next state: %w", projectcontext.Changed("project_context_close_extreme.go:89"))
	}
	project, err := r.store.Project(ctx)
	if err != nil || op.Target != "refs/heads/"+project.DefaultBranch {
		return fmt.Errorf("required close approved target branch: %w", projectcontext.Changed("project_context_close_extreme.go:93"))
	}
	expected := baseline
	expected.Operation = op
	stored, err := r.store.GetMarshalRun(ctx, r.ProjectID(), id)
	if err != nil || stored.Revision != source.marshalRevision+2 || !reflect.DeepEqual(stored.Value, expected) {
		return fmt.Errorf("required close exact journal revision: %w", projectcontext.Changed("project_context_close_extreme.go:99"))
	}
	release, err := admitRunControlLifecycle(r)
	if err != nil {
		return err
	}
	defer release()
	return r.Execution().Engine().WithRequiredContextFence(ctx, source.versions, func(snapshots map[string]execution.ExecutionRun) error {
		root, tasks, err := r.Execution().Engine().ValidateRequiredContextFenceCompletion(ctx, snapshots, source.rootRun.RunID)
		if err != nil || !reflect.DeepEqual(root, source.rootRun) {
			return fmt.Errorf("required close owned terminal run: %w", projectcontext.Changed("project_context_close_extreme.go:109"))
		}
		current := *source
		current.marshalRun, current.marshalRevision = stored.Value, stored.Revision
		if err = r.validateRequiredIntegrationSource(ctx, &current, root, tasks); err != nil {
			return err
		}
		if current.tree != source.tree || current.commonDir != source.commonDir {
			return fmt.Errorf("required close current source tree: %w", projectcontext.Changed("project_context_close_extreme.go:117"))
		}
		var goalID string
		var goalRevision int64
		var admissions []store.ProjectContextAdmission
		if checksOnly {
			if checksProof.goalID == "" || checksProof.goalRevision < 1 || len(checksProof.admissions) == 0 {
				return fmt.Errorf("required close checks-only context admissions: %w", projectcontext.Changed("project_context_close_extreme.go:checks-only-admissions"))
			}
			goalID, goalRevision, admissions = checksProof.goalID, checksProof.goalRevision, checksProof.admissions
		} else {
			session, err := r.store.GetVerificationSession(ctx, proof.session.ID)
			if err != nil || !reflect.DeepEqual(session, proof.session) || verification.Evaluate(session, session.Binding, time.Now().UTC()) != verification.VerifiedComplete {
				return fmt.Errorf("required close current verification session: %w", projectcontext.Changed("project_context_close_extreme.go:121"))
			}
			attestation, err := r.store.LatestCompletionAttestation(ctx, session.ID)
			if err != nil || attestation.Digest != proof.attestation.Digest || attestation.Verify(session.Binding) != nil {
				return fmt.Errorf("required close canonical attestation digest: %w", projectcontext.Changed("project_context_close_extreme.go:125"))
			}
			goalID, goalRevision, admissions = session.Binding.GoalID, session.Binding.GoalRevision, proof.admissions
		}
		goal, err := r.store.GetGoalContract(ctx, goalID, goalRevision)
		if err != nil {
			return err
		}
		grounding := &requiredProjectGroundingBudget{Hashes: map[string]string{}}
		for _, admission := range admissions {
			now := time.Now().UTC()
			snapshot, err := r.store.ReadProjectContextSnapshotBound(ctx, admission.Binding.Project, admission.Audience, admission.Selection, now, admission.Binding.Contract)
			if err != nil || snapshot.StateDigest != admission.ExpectedStateDigest {
				return fmt.Errorf("required close current mandatory memory: %w", projectcontext.Changed("project_context_close_extreme.go:136"))
			}
			for _, record := range snapshot.Records {
				inputs, err := requiredProjectFreshnessInputsBounded(source.workspace, source.head, record, grounding)
				if err != nil || r.memoryService.EvaluateFreshness(record, inputs).Classification != FreshnessFresh {
					return fmt.Errorf("required close fresh mandatory memory: %w", projectcontext.Changed("project_context_close_extreme.go:141"))
				}
			}
			packet, err := projectcontext.RenderWithPrefix(projectcontext.Manifest{Contract: snapshot.Contract, Goal: projectcontext.GoalFromCanonical(goal), Binding: admission.Binding, Selection: admission.Selection, Records: snapshot.Records}, admission.Prefix, admission.ByteCap)
			if err != nil || packet.Digest != admission.ExpectedPacketDigest || admission.ExpectedAuthority == nil {
				return fmt.Errorf("required close full context packet: %w", projectcontext.Changed("project_context_close_extreme.go:146"))
			}
			authority, err := r.store.ReadProjectContextAuthority(ctx, admission.ExpectedAuthority.Request, now)
			if err != nil || authority.Digest != admission.ExpectedAuthority.Digest {
				return fmt.Errorf("required close current SQL authority: %w", projectcontext.Changed("project_context_close_extreme.go:150"))
			}
		}
		if err = r.validateRequiredIntegrationSource(ctx, &current, root, tasks); err != nil {
			return err
		}
		before, err := gitMarshal(ctx, service.Repository, "rev-parse", op.Target)
		if err != nil || strings.TrimSpace(before) != op.Before {
			return fmt.Errorf("required close target base: %w", projectcontext.Changed("project_context_close_extreme.go:158"))
		}
		if checksOnly {
			if err = service.ensureChecksOnlyCloseFence(ctx, id, op); err != nil {
				return err
			}
		}
		return effect()
	})
}
