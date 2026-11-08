//go:build !governed

package app

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/verification"
)

// Canonical verification rows are data. Only the successful actual fresh
// verifier turn can create this same-runtime capability for final Attest.
type requiredCanonicalVerifierProof struct {
	mu                           sync.Mutex
	runtime                      *Runtime
	sourceObservation, principal string
	capture                      *requiredMarshalTurnCapture
	target                       *requiredArchitectureTurnTarget
	session                      verification.Session
	bundleDigest                 string
	consumed                     bool
	architecture                 *requiredArchitectureCompletionProof
}

func requiredVerifierKnownIdentity(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "unknown", "n/a", "unavailable", "unresolved", "none":
		return false
	}
	return true
}

func (r *Runtime) retainRequiredCanonicalVerifierProof(ctx context.Context, source *requiredIntegrationSource, session verification.Session, capture *requiredMarshalTurnCapture, target *requiredArchitectureTurnTarget, envelope verification.BundleEnvelope) error {
	principal, err := requiredArchitectureOwner(ctx, r)
	if err != nil {
		return err
	}
	if source == nil || source.runtime != r || capture == nil || target == nil || target.runtime != r || !target.verification || !target.prepared || target.consumed || target.cli == nil || capture.preparation.cli != target.cli || capture.preparation.runtime != r || !capture.consumed || !requiredVerifierKnownIdentity(capture.observedModel) || !requiredVerifierKnownIdentity(capture.session) || capture.priorSession != "" || capture.provider != target.provider || principal.ID != target.principal.ID || session.State != verification.VerifiedComplete || target.session.State != verification.Blocked || session.ID != target.session.ID || session.Version != target.session.Version+1 || session.Binding != target.session.Binding || source.rootRun.RunID != session.Binding.RunID || source.tree != session.Binding.TreeDigest || capture.preparation.tree != source.tree || capture.preparation.head != source.head || capture.preparation.receipt.ID != target.receipt.ID || capture.preparation.receipt.Binding != target.binding || capture.preparation.receipt.Stage != "review" || capture.preparation.receipt.Outcome != "QUALIFIED" || capture.binaryDigest == "" || capture.startedAt.Before(target.receipt.CheckedAt) || capture.finishedAt.Before(capture.startedAt) || session.UpdatedAt.Before(capture.finishedAt) {
		return projectcontext.Changed("project_context_verification_proof_extreme.go:45")
	}
	canonical, err := r.store.GetVerificationSession(ctx, session.ID)
	if err != nil || !reflect.DeepEqual(canonical, session) || envelope.Bundle.VerificationID != session.ID || envelope.Bundle.CreatedAt.Before(capture.finishedAt) || envelope.Bundle.Verify(envelope.Payloads, session.Binding) != nil {
		return projectcontext.Changed("project_context_verification_proof_extreme.go:49")
	}
	raw, err := json.Marshal(session)
	if err != nil {
		return err
	}
	var frozen verification.Session
	if err = json.Unmarshal(raw, &frozen); err != nil {
		return err
	}
	r.requiredIntegrationSources.mu.Lock()
	defer r.requiredIntegrationSources.mu.Unlock()
	observed := r.requiredIntegrationSources.byRun[session.Binding.RunID]
	if observed == nil || observed.runtime != r || observed.observationID != source.observationID || observed.verification != nil {
		return projectcontext.Changed("project_context_verification_proof_extreme.go:63")
	}
	observed.verification = &requiredCanonicalVerifierProof{runtime: r, sourceObservation: source.observationID, principal: principal.ID, capture: capture, target: target, session: frozen, bundleDigest: envelope.Bundle.ManifestDigest}
	return nil
}

func (r *Runtime) takeRequiredCanonicalVerifierProof(ctx context.Context, session verification.Session, envelope verification.BundleEnvelope) (*requiredCanonicalVerifierProof, error) {
	principal, err := requiredArchitectureOwner(ctx, r)
	if err != nil {
		return nil, err
	}
	r.requiredIntegrationSources.mu.Lock()
	source := r.requiredIntegrationSources.byRun[session.Binding.RunID]
	var proof *requiredCanonicalVerifierProof
	if source != nil {
		proof = source.verification
	}
	r.requiredIntegrationSources.mu.Unlock()
	if proof == nil || proof.runtime != r || proof.principal != principal.ID || proof.sourceObservation != source.observationID {
		return nil, projectcontext.ErrReviewPending
	}
	proof.mu.Lock()
	defer proof.mu.Unlock()
	if proof.consumed {
		return nil, projectcontext.Changed("project_context_verification_proof_extreme.go:87")
	}
	proof.consumed = true
	canonical, err := r.store.GetVerificationSession(ctx, session.ID)
	if err != nil || !reflect.DeepEqual(canonical, proof.session) || !reflect.DeepEqual(session, proof.session) || session.State != verification.VerifiedComplete || envelope.Bundle.ManifestDigest != proof.bundleDigest || envelope.Bundle.VerificationID != session.ID || envelope.Bundle.Verify(envelope.Payloads, session.Binding) != nil || verification.Evaluate(session, session.Binding, time.Now().UTC()) != verification.VerifiedComplete {
		return nil, projectcontext.Changed("project_context_verification_proof_extreme.go:92")
	}
	return proof, nil
}

func (p *requiredCanonicalVerifierProof) bindArchitecture(r *Runtime, architecture *requiredArchitectureCompletionProof) error {
	if p == nil || p.runtime != r || architecture == nil || architecture.runtime != r || architecture.capture == nil {
		return projectcontext.ErrMissing
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.consumed || p.architecture != nil || p.capture.provider == architecture.capture.provider && p.capture.session == architecture.capture.session {
		return projectcontext.Changed("project_context_verification_proof_extreme.go:104")
	}
	p.architecture = architecture
	architecture.verification = p
	return nil
}

// Called inside the existing owner/root/all-child fence; it never reenters
// Engine locks. No durable metadata or other runtime can satisfy this pointer.
func (p *requiredCanonicalVerifierProof) recheckFenced(r *Runtime, source *requiredIntegrationSource, architecture *requiredArchitectureCompletionProof, session verification.Session, envelope verification.BundleEnvelope) error {
	if p == nil || p.runtime != r || source == nil || source.runtime != r || architecture == nil {
		return projectcontext.ErrMissing
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.consumed || p.architecture != architecture || p.sourceObservation != source.observationID || !reflect.DeepEqual(session, p.session) || p.bundleDigest != envelope.Bundle.ManifestDigest || p.capture.preparation.head != source.head || p.capture.preparation.tree != source.tree || p.capture.preparation.runtime != r || !p.capture.consumed || p.capture.provider == architecture.capture.provider && p.capture.session == architecture.capture.session {
		return projectcontext.Changed("project_context_verification_proof_extreme.go:120")
	}
	return nil
}
