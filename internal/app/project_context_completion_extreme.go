//go:build !governed

package app

import (
	"context"
	"fmt"
	"reflect"
	"sync"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/verification"
)

type requiredArchitectureCompletionProof struct {
	runtime      *Runtime
	receipt      projectcontext.Receipt
	capture      *requiredMarshalTurnCapture
	target       *requiredArchitectureTurnTarget
	consumed     bool
	mu           sync.Mutex
	verification *requiredCanonicalVerifierProof
}

func (s *VerificationService) completeRequiredContextAttestation(ctx context.Context, session verification.Session, envelope verification.BundleEnvelope, provenance string) (verification.CompletionAttestation, error) {
	if s == nil || s.runtime == nil {
		return verification.CompletionAttestation{}, projectcontext.ErrMissing
	}
	provider := ""
	installed := installedMarshalProviders()
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
		return verification.CompletionAttestation{}, projectcontext.ErrReviewPending
	}
	cli := freshRoleCLI(provider, "")
	cli.runtime, cli.ProjectID = s.runtime, s.runtime.ProjectIdentity()
	return s.completeRequiredContextAttestationWithCLI(ctx, session, envelope, provenance, cli)
}

func (s *VerificationService) completeRequiredContextAttestationWithCLI(ctx context.Context, session verification.Session, envelope verification.BundleEnvelope, provenance string, cli *MarshalCLI) (verification.CompletionAttestation, error) {
	zero := verification.CompletionAttestation{}
	if s == nil || s.runtime == nil {
		return zero, projectcontext.ErrMissing
	}
	r := s.runtime
	verifier, err := r.takeRequiredCanonicalVerifierProof(ctx, session, envelope)
	if err != nil {
		return zero, err
	}
	var proof *requiredArchitectureCompletionProof
	receipt, err := r.produceRequiredArchitectureReview(ctx, cli, session.ID, envelope, &proof)
	if err != nil {
		return zero, err
	}
	if receipt.Outcome != "COMPATIBLE" || proof == nil {
		return zero, projectcontext.ErrReviewPending
	}
	if err = verifier.bindArchitecture(r, proof); err != nil {
		return zero, err
	}
	return s.commitRequiredContextCompletionProof(ctx, session, envelope, provenance, proof)
}

// Serialized COMPATIBLE receipts cannot construct this live successful-turn
// proof. A rightful-owner attempt spends it BEFORE currentness checks; foreign
// runtimes cannot spend the original owner's proof. No model runs under locks.
func (s *VerificationService) commitRequiredContextCompletionProof(ctx context.Context, session verification.Session, envelope verification.BundleEnvelope, provenance string, proof *requiredArchitectureCompletionProof) (verification.CompletionAttestation, error) {
	zero := verification.CompletionAttestation{}
	if s == nil || s.runtime == nil || proof == nil || proof.runtime != s.runtime || proof.capture == nil || proof.target == nil || proof.capture.preparation.runtime != s.runtime || proof.verification == nil || proof.verification.runtime != s.runtime {
		return zero, projectcontext.ErrMissing
	}
	proof.mu.Lock()
	defer proof.mu.Unlock()
	if proof.consumed {
		return zero, projectcontext.Changed("project_context_completion_extreme.go:86")
	}
	proof.consumed = true
	r := s.runtime
	if proof.receipt.Outcome != "COMPATIBLE" || !proof.capture.consumed || proof.capture.priorSession != "" || !requiredVerifierKnownIdentity(proof.capture.session) || !requiredVerifierKnownIdentity(proof.capture.observedModel) || proof.capture.preparation.receipt.ID != proof.receipt.ParentReceiptID || proof.capture.preparation.receipt.Binding != proof.receipt.Binding || proof.target.receipt.ID != proof.receipt.ParentReceiptID || !reflect.DeepEqual(proof.target.session, session) {
		return zero, fmt.Errorf("completion live capture: %w", projectcontext.Changed("project_context_completion_extreme.go:91"))
	}
	principal, err := requiredArchitectureOwner(ctx, r)
	if err != nil || principal.ID != proof.receipt.Binding.Principal {
		return zero, projectcontext.Changed("project_context_completion_extreme.go:95")
	}
	release, err := admitRunControlLifecycle(r)
	if err != nil {
		return zero, err
	}
	defer release()
	var attestation verification.CompletionAttestation
	err = r.Execution().Engine().WithRequiredContextFence(ctx, proof.target.versions, func(snapshots map[string]execution.ExecutionRun) error {
		source, err := r.requiredCompletionSourceFenced(ctx, snapshots, session.Binding.RunID)
		if err != nil {
			return fmt.Errorf("completion owned source: %w", err)
		}
		if source.head != proof.capture.preparation.head || source.tree != session.Binding.TreeDigest || source.tree != proof.capture.preparation.tree {
			return fmt.Errorf("completion source capture: %w", projectcontext.Changed("project_context_completion_extreme.go:109"))
		}
		if err = proof.verification.recheckFenced(r, source, proof, session, envelope); err != nil {
			return err
		}
		current, err := r.store.GetVerificationSession(ctx, session.ID)
		if err != nil || !reflect.DeepEqual(current, session) {
			return fmt.Errorf("completion canonical session: %w", projectcontext.Changed("project_context_completion_extreme.go:116"))
		}
		now := s.now()
		if now.Before(proof.receipt.CheckedAt) || verification.Evaluate(session, session.Binding, now) != verification.VerifiedComplete {
			return fmt.Errorf("completion issuance: %w", projectcontext.Changed("project_context_completion_extreme.go:120"))
		}
		attestation, err = verification.NewCompletionAttestation("completion-"+session.ID+fmt.Sprintf("-%d", session.Version), session, envelope.Bundle.ManifestDigest, provenance, now)
		if err != nil {
			return err
		}
		p, err := r.store.GetActivePlan(ctx, source.rootRun.ProjectID)
		if err != nil {
			return err
		}
		accepted, err := r.store.ReadAcceptedProjectContract(ctx, r.ProjectContractBinding())
		if err != nil {
			return err
		}
		ref := projectcontext.ContractRef{ID: accepted.Contract.ID, Revision: accepted.Contract.Revision, Digest: accepted.Digest}
		if ref != proof.receipt.CurrentContract {
			return projectcontext.Changed("project_context_completion_extreme.go:136")
		}
		audience, err := r.projectContractOwnerAudience(ctx, principal.ID)
		if err != nil {
			return err
		}
		goal, err := r.store.GetGoalContract(ctx, session.Binding.GoalID, session.Binding.GoalRevision)
		if err != nil {
			return err
		}
		admissions := make([]store.ProjectContextAdmission, 0, len(p.Tasks))
		grounding := &requiredProjectGroundingBudget{Hashes: map[string]string{}}
		// The review covered all mandatory scopes. Recheck that entire context
		// here as well as each task; future-task memory cannot fall between them.
		review := proof.capture.preparation.admission
		review.Now = now
		input, err := r.store.ReadProjectArchitectureReview(ctx, review, session.ID, session.Version, envelope)
		if err != nil || input.Digest != proof.target.input.Digest {
			return fmt.Errorf("completion full architecture context: %w", projectcontext.Changed("project_context_completion_extreme.go:154"))
		}
		full, err := r.store.ReadProjectContextSnapshotBound(ctx, review.Binding.Project, review.Audience, review.Selection, now, review.Binding.Contract)
		if err != nil || full.StateDigest != review.ExpectedStateDigest {
			return projectcontext.Changed("project_context_completion_extreme.go:158")
		}
		for _, record := range full.Records {
			inputs, err := requiredProjectFreshnessInputsBounded(source.workspace, source.head, record, grounding)
			if err != nil || r.memoryService.EvaluateFreshness(record, inputs).Classification != FreshnessFresh {
				return projectcontext.Changed("project_context_completion_extreme.go:163")
			}
		}
		for _, task := range p.Tasks {
			b := proof.receipt.Binding
			b.TaskID, b.CanonicalTaskID = task.ID, canonicalPlanTaskID(p.ID, p.Version, task.ID)
			b.InventoryDigest, b.ManifestDigest, b.RequiredStateDigest = "", "", ""
			selection, err := requiredProjectPlanSelection(p, b, accepted.Contract)
			if err != nil {
				return err
			}
			snapshot, err := r.store.ReadProjectContextSnapshotBound(ctx, b.Project, audience, selection, now, b.Contract)
			if err != nil {
				return err
			}
			for _, record := range snapshot.Records {
				inputs, err := requiredProjectFreshnessInputsBounded(source.workspace, source.head, record, grounding)
				if err != nil {
					return err
				}
				if r.memoryService.EvaluateFreshness(record, inputs).Classification != FreshnessFresh {
					return projectcontext.Changed("project_context_completion_extreme.go:184")
				}
			}
			b.RequiredStateDigest = snapshot.StateDigest
			packet, err := projectcontext.Render(projectcontext.Manifest{Contract: snapshot.Contract, Goal: projectcontext.GoalFromCanonical(goal), Binding: b, Selection: selection, Records: snapshot.Records}, projectcontext.MaximumPacketBytes)
			if err != nil {
				return err
			}
			request := store.ProjectContextAuthorityRequest{Binding: b, Purpose: store.ProjectContextAuthorityCompletion}
			if r.policyConfigured {
				request.RuntimePolicyID, request.RuntimePolicyVersion = string(r.runtimePolicy.PolicyID), int64(r.runtimePolicy.PolicyVersion)
			}
			authority, err := r.store.ReadProjectContextAuthority(ctx, request, now)
			if err != nil {
				return fmt.Errorf("completion task %s authority: %w", task.ID, err)
			}
			id, err := model.NewID("CTX-COMPLETE-")
			if err != nil {
				return err
			}
			admissions = append(admissions, store.ProjectContextAdmission{ID: id, Stage: "completion", Binding: b, Audience: audience, Selection: selection, ExpectedStateDigest: snapshot.StateDigest, ExpectedPacketDigest: packet.Digest, ByteCap: projectcontext.MaximumPacketBytes, Now: now, ExpectedAuthority: &authority})
		}
		// Source is rehashed before the short SQL writer transaction. Store then
		// atomically rechecks all Goal/Plan/context/authority/verifier rows.
		if _, err = r.requiredCompletionSourceFenced(ctx, snapshots, session.Binding.RunID); err != nil {
			return err
		}
		_, err = r.store.CommitProjectContextCompletion(ctx, admissions, envelope, attestation, proof.receipt.ID)
		if err != nil {
			return fmt.Errorf("completion atomic coverage: %w", err)
		}
		return r.retainRequiredContextCloseProof(source, proof, session, attestation, admissions)
	})
	if err != nil {
		return zero, err
	}
	return attestation, nil
}
