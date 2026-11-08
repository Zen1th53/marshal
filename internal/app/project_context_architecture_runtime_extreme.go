//go:build !governed

package app

import (
	"context"
	"encoding/json"
	"reflect"
	"time"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/verification"
)

func (t *requiredArchitectureTurnTarget) architectureProof(snapshots map[string]execution.ExecutionRun) requiredProjectWorkspaceProof {
	r := t.runtime
	observe := func(ctx context.Context, b projectcontext.Binding) (store.ProjectContextAuthorityObservation, error) {
		request := store.ProjectContextAuthorityRequest{Binding: b, Purpose: store.ProjectContextAuthorityCompletion}
		if r.policyConfigured {
			request.RuntimePolicyID, request.RuntimePolicyVersion = string(r.runtimePolicy.PolicyID), int64(r.runtimePolicy.PolicyVersion)
		}
		return r.store.ReadProjectContextAuthority(ctx, request, time.Now().UTC())
	}
	return requiredProjectWorkspaceProof{Runtime: r, OwnerPlanning: true, ArchitectureReview: t, SQLAuthority: observe, Read: func(ctx context.Context, b projectcontext.Binding) (requiredProjectWorkspaceState, error) {
		if !t.matches(r, b) {
			return requiredProjectWorkspaceState{}, projectcontext.Changed("project_context_architecture_runtime_extreme.go:30")
		}
		if principal, err := requiredArchitectureOwner(ctx, r); err != nil || principal.ID != b.Principal {
			return requiredProjectWorkspaceState{}, projectcontext.Changed("project_context_architecture_runtime_extreme.go:33")
		}
		source, err := r.requiredCompletionSourceFenced(ctx, snapshots, b.RunID)
		if err != nil {
			return requiredProjectWorkspaceState{}, err
		}
		if source.workspace != t.workspace || source.head != t.head || source.tree != t.tree {
			return requiredProjectWorkspaceState{}, projectcontext.Changed("project_context_architecture_runtime_extreme.go:40")
		}
		current, err := r.store.GetVerificationSession(ctx, t.session.ID)
		if err != nil || !reflect.DeepEqual(current, t.session) {
			return requiredProjectWorkspaceState{}, projectcontext.Changed("project_context_architecture_runtime_extreme.go:44")
		}
		authority, err := observe(ctx, b)
		if err != nil {
			return requiredProjectWorkspaceState{}, err
		}
		digest := projectcontext.Digest("marshal.required.architecture-source.v1", struct {
			Source, Session, Authority string
			Snapshots                  map[string]execution.ExecutionRun
		}{source.observationID, projectcontext.Digest("session", current), authority.Digest, snapshots})
		return requiredProjectWorkspaceState{Workspace: source.workspace, Base: b.Base, Head: source.head, Tree: source.tree, AuthorityDigest: digest, SQLAuthorityDigest: authority.Digest}, nil
	}}
}
func (t *requiredArchitectureTurnTarget) recheck(ctx context.Context) error {
	r := t.runtime
	release, err := admitRunControlLifecycle(r)
	if err != nil {
		return err
	}
	defer release()
	return r.Execution().Engine().WithRequiredContextFence(ctx, t.versions, func(snapshots map[string]execution.ExecutionRun) error {
		proof := t.architectureProof(snapshots)
		state, err := proof.Read(ctx, t.binding)
		if err != nil {
			return err
		}
		return requiredProjectWorkspaceCurrent(ctx, t.binding, state)
	})
}

type requiredArchitectureVerdict struct {
	Head           string `json:"head"`
	Tree           string `json:"tree"`
	ContractDigest string `json:"contract_digest"`
	Assessments    []struct {
		ObligationID string   `json:"obligation_id"`
		Status       string   `json:"status"`
		Reason       string   `json:"reason"`
		EvidenceRefs []string `json:"evidence_refs"`
	} `json:"assessments"`
}

// requiredArchitectureProcessRule tells the final compatibility assessor how
// to judge the parts of a Goal that describe how the run is carried out
// rather than what the source must contain. An e2e Goal read "... using
// claude and opencode workers with cross-provider review, then deliver to
// main and close"; the assessor answered UNKNOWN because source cannot show
// delivery, which by construction happens only after this assessment, and
// the run could never close.
const requiredArchitectureProcessRule = "Judge the source outcome of each Goal obligation from the integrated source. Parts of a Goal that describe how the run is carried out are not source facts: delivery to the target branch and closing the run happen only after this assessment passes, so they are never a reason for UNKNOWN or FAIL here; which workers did the tasks and which providers reviewed them are runtime facts, judged only from the supplied Session and Bundle evidence (provider observations, reviews), never required to appear in source. A Goal constraint, explicit limit or do-not-do item is an obligation the result must honour: it is PASS when the integrated result does not violate it, with a reference showing that it is not violated, and FAIL when the result violates it. N/A is only for an obligation that cannot apply to this Goal's result at all; a limit the result honours is never N/A. "

const requiredArchitectureSchema = `{"type":"object","additionalProperties":false,"properties":{"head":{"type":"string"},"tree":{"type":"string"},"contract_digest":{"type":"string"},"assessments":{"type":"array","items":{"type":"object","additionalProperties":false,"properties":{"obligation_id":{"type":"string"},"status":{"type":"string","enum":["PASS","FAIL","UNKNOWN","N/A"]},"reason":{"type":"string","maxLength":2000},"evidence_refs":{"type":"array","items":{"type":"string"}}},"required":["obligation_id","status","reason","evidence_refs"]}}},"required":["head","tree","contract_digest","assessments"]}`

func (r *Runtime) runRequiredArchitectureReviewWithCLI(ctx context.Context, cli *MarshalCLI, verificationID string, envelope verification.BundleEnvelope) (projectcontext.Receipt, error) {
	return r.produceRequiredArchitectureReview(ctx, cli, verificationID, envelope, nil)
}

func (r *Runtime) produceRequiredArchitectureReview(ctx context.Context, cli *MarshalCLI, verificationID string, envelope verification.BundleEnvelope, completion **requiredArchitectureCompletionProof) (projectcontext.Receipt, error) {
	zero := projectcontext.Receipt{}
	principal, err := requiredArchitectureOwner(ctx, r)
	if err != nil {
		return zero, err
	}
	if cli == nil || cli.runtime != r || cli.ProjectID != r.ProjectIdentity() || cli.ConversationID != "" {
		return zero, projectcontext.ErrReviewPending
	}
	session, err := r.store.GetVerificationSession(ctx, verificationID)
	if err != nil {
		return zero, err
	}
	current, err := r.Verification().BindingForRun(ctx, session.Binding.RunID)
	if err != nil || session.Binding != current || session.State != verification.VerifiedComplete || verification.Evaluate(session, current, time.Now().UTC()) != verification.VerifiedComplete || envelope.Bundle.VerificationID != session.ID {
		return zero, projectcontext.Changed("project_context_architecture_runtime_extreme.go:116")
	}
	if err = envelope.Bundle.Verify(envelope.Payloads, current); err != nil {
		return zero, err
	}
	root, err := r.Execution().Engine().GetRun(ctx, current.RunID)
	if err != nil {
		return zero, err
	}
	versions, err := requiredCompletionVersions(ctx, r.Execution().Engine(), root)
	if err != nil {
		return zero, err
	}
	p, err := r.store.GetActivePlan(ctx, root.ProjectID)
	if err != nil || p.ID != root.PlanID || p.Version != root.PlanVersion || p.State != plan.StateApproved {
		return zero, projectcontext.Changed("project_context_architecture_runtime_extreme.go:131")
	}
	accepted, err := r.store.ReadAcceptedProjectContract(ctx, r.ProjectContractBinding())
	if err != nil {
		return zero, err
	}
	selection, err := projectcontext.PlanningSelection(accepted.Contract)
	if err != nil {
		return zero, err
	}
	workspace, head, tree, err := r.resolveRequiredCompletionSource(ctx, root)
	if err != nil {
		return zero, err
	}
	cli.Dir = workspace
	b := projectcontext.Binding{FormatVersion: projectcontext.FormatVersion, Project: r.ProjectContractBinding(), Contract: projectcontext.ContractRef{ID: accepted.Contract.ID, Revision: accepted.Contract.Revision, Digest: accepted.Digest}, GoalID: root.GoalID, GoalRevision: root.GoalRevision, PlanID: root.PlanID, PlanVersion: root.PlanVersion, ApprovalScopeDigest: p.ApprovalScopeDigest, RunID: root.RunID, RunVersion: root.Version, Principal: principal.ID, Session: root.SessionID, Base: head, Tree: tree}
	audience, err := r.projectContractOwnerAudience(ctx, principal.ID)
	if err != nil {
		return zero, err
	}
	planning, err := r.store.ReadProjectPlanningContext(ctx, store.ProjectPlanningContextRequest{Binding: b, Audience: audience, ByteCap: requiredProviderEnvelopeBytes, Now: time.Now().UTC()})
	if err != nil {
		return zero, err
	}
	b = planning.Binding
	a := store.ProjectContextAdmission{Binding: b, Selection: selection, Audience: audience, ByteCap: requiredProviderEnvelopeBytes, ExpectedStateDigest: planning.Snapshot.StateDigest, ExpectedPacketDigest: planning.Packet.Digest, Now: time.Now().UTC()}
	input, err := r.store.ReadProjectArchitectureReview(ctx, a, session.ID, session.Version, envelope)
	if err != nil {
		return zero, err
	}
	target := &requiredArchitectureTurnTarget{runtime: r, cli: cli, principal: principal, binding: b, session: session, envelope: envelope, versions: versions, provider: cli.Provider, workspace: workspace, head: head, tree: tree, admission: a, input: input}
	cli.requiredArchitectureTarget = target
	defer func() { target.consumed = true; cli.requiredArchitectureTarget = nil }()
	// The manifest alone contains hashes, not the accepted review facts.
	// Supply the verified payloads as JSON so the assessor can read and cite
	// each bundle-entry reference, including task cross-provider acceptances.
	payloads := make(map[string]json.RawMessage, len(envelope.Payloads))
	for path, raw := range envelope.Payloads {
		payloads[path] = json.RawMessage(raw)
	}
	fullInput, err := json.MarshalIndent(struct {
		Head, Tree, ContractDigest string
		Tasks                      []plan.Task
		Session                    verification.Session
		Bundle                     verification.EvidenceBundle
		BundlePayloads             map[string]json.RawMessage
		Obligations, EvidenceRefs  []string
	}{head, tree, b.Contract.Digest, p.Tasks, session, envelope.Bundle, payloads, input.Obligations, input.EvidenceRefs}, "", "  ")
	if err != nil {
		return zero, err
	}
	// The complete runtime input (the verified session with its full runtime
	// proof, the evidence bundle and every approved task) is a read-only file
	// bound by its sha256; the prompt keeps a compact view within the context
	// envelope. A changed file is refused after the turn.
	inputFiles, err := newVerifierProofFiles()
	if err != nil {
		return zero, err
	}
	defer inputFiles.cleanup()
	prefixData, err := inputFiles.compactArchitectureInput(head, tree, b.Contract.Digest, p.Tasks, input.Obligations, input.EvidenceRefs, fullInput)
	if err != nil {
		return zero, err
	}
	prompt := "Independently assess the exact integrated source against EVERY supplied architecture, Goal, task and mandatory memory obligation. Read current in-worktree files as needed. Return one assessment per canonical obligation with PASS/FAIL/UNKNOWN/N/A, a concrete reason, its obligation reference and a resolvable source/evidence reference. UNKNOWN when evidence is insufficient. Never assert COMPATIBLE or completion authority. " + requiredArchitectureProcessRule + architectureInputFileInstruction + "Exact runtime input: " + string(prefixData)
	var verdict requiredArchitectureVerdict
	if err = cli.turn(ctx, prompt, requiredArchitectureSchema, &verdict); err != nil {
		return zero, err
	}
	if err = inputFiles.verify(); err != nil {
		return zero, err
	}
	capture, err := cli.takeRequiredMarshalTurnCapture(r)
	if err != nil {
		return zero, err
	}
	if capture.preparation.cli != cli || capture.preparation.runtime != r || capture.preparation.receipt.ID != target.receipt.ID || capture.preparation.receipt.Binding != target.binding || capture.preparation.head != head || capture.preparation.tree != tree || capture.provider != cli.Provider || verdict.Head != head || verdict.Tree != tree || verdict.ContractDigest != b.Contract.Digest {
		return zero, projectcontext.Changed("project_context_architecture_runtime_extreme.go:184")
	}
	id, err := model.NewID("CTX-ARCH-")
	if err != nil {
		return zero, err
	}
	invocationID, err := model.NewID("ARCH-INVOKE-")
	if err != nil {
		return zero, err
	}
	observation := store.ProjectArchitectureObservation{ID: id, VerificationID: session.ID, VerificationVersion: session.Version, InputDigest: target.input.Digest, Invocation: store.ProjectArchitectureInvocation{ID: invocationID, Provider: capture.provider, ObservedModel: capture.observedModel, SessionID: capture.session, PriorSessionID: capture.priorSession, BinaryDigest: capture.binaryDigest, InputDigest: capture.inputDigest, OutputDigest: capture.outputDigest, PayloadDigest: capture.payloadDigest, SourceHead: head, WorkspaceTree: tree, ContextReceiptID: target.receipt.ID, StartedAt: capture.startedAt, FinishedAt: capture.finishedAt}}
	for _, assessment := range verdict.Assessments {
		observation.Assessments = append(observation.Assessments, store.ProjectArchitectureAssessment{ObligationID: assessment.ObligationID, Status: assessment.Status, Reason: assessment.Reason, EvidenceRefs: assessment.EvidenceRefs})
	}
	release, err := admitRunControlLifecycle(r)
	if err != nil {
		return zero, err
	}
	defer release()
	var receipt projectcontext.Receipt
	err = r.Execution().Engine().WithRequiredContextFence(ctx, versions, func(snapshots map[string]execution.ExecutionRun) error {
		proof := target.architectureProof(snapshots)
		if _, err := proof.Read(ctx, target.binding); err != nil {
			return err
		}
		a := target.admission
		a.Now = time.Now().UTC()
		receipt, err = r.store.CommitProjectArchitectureCompatibility(ctx, a, observation, envelope)
		return err
	})
	if err != nil {
		return zero, err
	}
	if completion != nil && receipt.Outcome == "COMPATIBLE" {
		*completion = &requiredArchitectureCompletionProof{runtime: r, receipt: receipt, capture: capture, target: target}
	}
	return receipt, nil
}

// A fresh independently selected provider/session is required. Claude's native
// output currently reports an observed model; other absent model metadata is
// UNKNOWN. Native JSON cannot choose this private CLI or executable.
func (r *Runtime) runRequiredArchitectureReview(ctx context.Context, verificationID string, envelope verification.BundleEnvelope) (projectcontext.Receipt, error) {
	installed := installedMarshalProviders()
	provider := ""
	for _, candidate := range installed {
		if candidate == "claude" {
			provider = candidate
			break
		}
	}
	if provider == "" && len(installed) > 0 {
		provider = installed[0]
	}
	if provider == "" {
		return projectcontext.Receipt{}, projectcontext.ErrReviewPending
	}
	cli := freshRoleCLI(provider, "")
	cli.runtime, cli.ProjectID = r, r.ProjectIdentity()
	return r.runRequiredArchitectureReviewWithCLI(ctx, cli, verificationID, envelope)
}
