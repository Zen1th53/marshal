package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/budget"
	"github.com/Zen1th53/marshal/internal/evidence"
	"github.com/Zen1th53/marshal/internal/memory/security"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/verification"
)

type VerificationActionRequest struct {
	Operation                              string
	ExpectedDigest                         string
	ClaimID, EvidenceID, FindingID, Reason string
	ExpiresAt                              time.Time
	Destination                            string
	BundleDigest                           string
}
type VerificationActionResult struct {
	Session        verification.Session
	Attestation    *verification.CompletionAttestation
	Bundle         *MemoryFileBinding
	ManifestDigest string
}
type VerificationActionReview struct {
	Session      verification.Session
	Digest       string
	BundleDigest string
	ExportDigest string
	ExportSize   int64
}

func verificationSessionDigest(session verification.Session) string {
	body, _ := json.Marshal(session)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
func (r *Runtime) ReviewVerificationAction(ctx context.Context, id string) (VerificationActionReview, error) {
	var review VerificationActionReview
	p, ok := auth.LocalFromContext(ctx)
	if r == nil || !ok || ctx.Value(localControlKey{}) != r || p.ProjectID() != r.ProjectIdentity() {
		return review, authz.ErrDenied
	}
	session, err := r.Verification().Current(ctx, id)
	if err != nil {
		return review, err
	}
	if session.Binding.ProjectID != r.ProjectIdentity() {
		return review, authz.ErrDenied
	}
	binding, err := r.Verification().BindingForRun(ctx, session.Binding.RunID)
	if err != nil {
		return review, err
	}
	if binding != session.Binding {
		return review, verification.ErrBindingMismatch
	}
	review.Session = session
	review.Digest = verificationSessionDigest(session)
	envelope, err := r.verificationEnvelope(ctx, session)
	if err != nil {
		return review, err
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return review, err
	}
	sum := sha256.Sum256(body)
	review.BundleDigest = envelope.Bundle.ManifestDigest
	review.ExportDigest = hex.EncodeToString(sum[:])
	review.ExportSize = int64(len(body))
	return review, nil
}

// verificationEnvelope exports canonical Process06 evidence metadata and its
// references. It is a real verified BundleEnvelope, never a Process05 handoff
// or a claim that external logs/tests have been rerun by exporting them.
func (r *Runtime) verificationEnvelope(ctx context.Context, session verification.Session) (verification.BundleEnvelope, error) {
	var envelope verification.BundleEnvelope
	body, err := json.Marshal(session)
	if err != nil {
		return envelope, err
	}
	// Community refuses an export holding a secret; Extreme exports it with
	// the secret removed (every payload, evidence included).
	firewall := security.NewFirewall(security.FirewallConfig{})
	if body, _, err = firewall.AdmitJSON(body); err != nil {
		return envelope, err
	}
	payloads := map[string][]byte{"verification.json": body}
	if err := addRuntimeCheckOutputs(payloads, session.RuntimeProof); err != nil {
		return envelope, err
	}
	for path, payload := range payloads {
		if path == "verification.json" {
			continue
		}
		// Exports apply the same secret admission policy to complete outputs.
		admitted, _, err := firewall.AdmitJSON(payload)
		if err != nil {
			return envelope, err
		}
		payloads[path] = admitted
	}
	for _, ev := range session.Evidence {
		if len(ev.ID) > 256 {
			return envelope, model.ErrInvalid
		}
		// Digests and provenance remain metadata. Raw provider transcripts and
		// hidden reasoning are never loaded into this export path.
		raw, err := json.Marshal(ev)
		if err != nil {
			return envelope, err
		}
		if security.RedactsSecrets {
			if raw, _, err = firewall.AdmitJSON(raw); err != nil {
				return envelope, err
			}
		}
		sum := sha256.Sum256([]byte(ev.ID))
		payloads["evidence/"+hex.EncodeToString(sum[:])+".json"] = raw
	}
	var entries []verification.BundleEntry
	for path, payload := range payloads {
		sum := sha256.Sum256(payload)
		entries = append(entries, verification.BundleEntry{Path: path, Digest: hex.EncodeToString(sum[:]), Size: int64(len(payload))})
	}
	bundle, err := verification.BuildEvidenceBundle(fmt.Sprintf("bundle-%s-%d", session.ID, session.Version), session.ID, session.Binding, entries, session.UpdatedAt)
	if err != nil {
		return envelope, err
	}
	envelope = verification.BundleEnvelope{Bundle: bundle, Payloads: payloads}
	if err = bundle.Verify(payloads, session.Binding); err != nil {
		return envelope, err
	}
	return envelope, nil
}

func (r *Runtime) CommandVerification(ctx context.Context, e CommandEnvelope, req VerificationActionRequest) (VerificationActionResult, error) {
	var result VerificationActionResult
	operation := "verification." + req.Operation
	switch req.Operation {
	case "challenge", "close-finding", "attest", "build-bundle", "export-bundle":
	default:
		return result, model.ErrInvalid
	}
	ctx, principal, command, err := r.memoryCommand(ctx, e, operation, req)
	if err != nil {
		return result, err
	}
	session, err := r.Verification().Current(ctx, e.TargetID)
	if err != nil {
		return result, err
	}
	if session.Binding.ProjectID != e.ProjectID {
		return result, authz.ErrDenied
	}
	if version, found, err := r.store.MemoryCommandResult(ctx, command); err != nil {
		return result, err
	} else if found {
		if session.Version != version {
			return result, verification.ErrConflict
		}
		result.Session = session
		switch req.Operation {
		case "attest":
			a, err := r.Verification().Attestation(ctx, session.ID)
			if err != nil {
				return result, err
			}
			result.Attestation = &a
		case "build-bundle", "export-bundle":
			path := req.Destination
			if req.Operation == "build-bundle" {
				path = r.verificationBundlePath(session)
			}
			_, file, err := r.readMemoryActionFile(path)
			if err != nil {
				return result, err
			}
			result.Bundle = &file
			result.ManifestDigest = req.BundleDigest
		}
		return result, nil
	}
	review, err := r.ReviewVerificationAction(ctx, session.ID)
	if err != nil {
		return result, err
	}
	if e.ExpectedVersion != session.Version || req.ExpectedDigest != review.Digest {
		return result, verification.ErrConflict
	}
	switch req.Operation {
	case "challenge":
		node, err := r.store.Get(ctx, evidence.NodeID(req.EvidenceID))
		if err != nil {
			return result, err
		}
		if node.State != evidence.StateStored && node.State != evidence.StateLinked {
			return result, verification.ErrInvalid
		}
		if node.Metadata["project_id"] != e.ProjectID || (node.Metadata["run_id"] != "" && node.Metadata["run_id"] != session.Binding.RunID) {
			return result, authz.ErrDenied
		}
		// Community refuses a reason holding a secret; Extreme records the
		// challenge with the secret removed.
		if req.Reason, _, err = security.NewFirewall(security.FirewallConfig{}).AdmitText(req.Reason); err != nil {
			return result, err
		}
		next, err := verification.Challenge(session, verification.ChallengeInput{ClaimID: req.ClaimID, EvidenceID: req.EvidenceID, EvidenceDigest: node.Digest, Reason: req.Reason}, time.Now().UTC())
		if err != nil {
			return result, err
		}
		if err = r.store.UpdateVerificationSession(ctx, next, session.Version); err != nil {
			return result, err
		}
		result.Session, err = r.Verification().Current(ctx, session.ID)
		return result, err
	case "close-finding":
		next, err := verification.CloseFinding(session, verification.FindingClosure{FindingID: req.FindingID, Actor: principal.ID, Reason: req.Reason, ExpiresAt: req.ExpiresAt}, time.Now().UTC())
		if err != nil {
			return result, err
		}
		if err = r.store.UpdateVerificationSession(ctx, next, session.Version); err != nil {
			return result, err
		}
		result.Session, err = r.Verification().Current(ctx, session.ID)
		return result, err
	case "attest":
		envelope, err := r.readVerificationEnvelope(ctx, session)
		if err != nil {
			return result, err
		}
		if envelope.Bundle.ManifestDigest != req.BundleDigest {
			return result, verification.ErrBindingMismatch
		}
		a, err := r.Verification().Attest(ctx, session.ID, envelope, "local-operator:"+principal.ID)
		if err != nil {
			return result, err
		}
		result.Session = session
		result.Attestation = &a
		return result, nil
	case "build-bundle", "export-bundle":
		envelope, err := r.verificationEnvelope(ctx, session)
		if err != nil {
			return result, err
		}
		if req.BundleDigest != review.BundleDigest {
			return result, verification.ErrConflict
		}
		if req.Operation == "export-bundle" {
			envelope, err = r.readVerificationEnvelope(ctx, session)
			if err != nil {
				return result, err
			}
		}
		body, err := json.Marshal(envelope)
		if err != nil {
			return result, err
		}
		path := req.Destination
		if req.Operation == "build-bundle" {
			path = r.verificationBundlePath(session)
			if err = r.ensureVerificationExportDirectory(); err != nil {
				return result, err
			}
		}
		file, err := r.writeMemoryActionFile(path, body)
		if os.IsExist(err) {
			existing, binding, readErr := r.readMemoryActionFile(path)
			if readErr == nil && string(existing) == string(body) {
				file = binding
				err = nil
			}
		}
		if err != nil {
			return result, err
		}
		if err = r.store.RecordVerificationActionReceipt(ctx, session); err != nil {
			return result, err
		}
		reread, err := r.Verification().Current(ctx, session.ID)
		if err != nil {
			return result, err
		}
		result.Session = reread
		result.Bundle = &file
		result.ManifestDigest = envelope.Bundle.ManifestDigest
		return result, nil
	}
	return result, model.ErrInvalid
}
func (r *Runtime) verificationBundlePath(session verification.Session) string {
	digest := verificationSessionDigest(session)
	return filepath.Join(".marshal", "exports", "verification-"+digest+".json")
}
func (r *Runtime) ensureVerificationExportDirectory() error {
	root, err := os.OpenRoot(r.layout.RuntimeDir)
	if err != nil {
		return err
	}
	defer root.Close()
	info, err := root.Lstat("exports")
	if os.IsNotExist(err) {
		return root.Mkdir("exports", 0700)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return authz.ErrDenied
	}
	return nil
}
func (r *Runtime) readVerificationEnvelope(ctx context.Context, session verification.Session) (verification.BundleEnvelope, error) {
	var envelope verification.BundleEnvelope
	body, _, err := r.readMemoryActionFile(r.verificationBundlePath(session))
	if err != nil {
		return envelope, err
	}
	if err = json.Unmarshal(body, &envelope); err != nil {
		return envelope, err
	}
	if envelope.Bundle.VerificationID != session.ID {
		return envelope, verification.ErrBindingMismatch
	}
	if err = envelope.Bundle.Verify(envelope.Payloads, session.Binding); err != nil {
		return envelope, err
	}
	current, err := r.verificationEnvelope(ctx, session)
	if err != nil {
		return envelope, err
	}
	if current.Bundle.ManifestDigest != envelope.Bundle.ManifestDigest {
		return envelope, verification.ErrBindingMismatch
	}
	return envelope, nil
}

// ReserveVerificationCall must run immediately before a Process06 provider
// launch. Evaluate/challenge/bundle/attest are local checks and consume no model
// calls. A false admission is an ambiguous/replayed launch and must not run.
func (s *VerificationService) ReserveVerificationCall(ctx context.Context, id, key string, maxTokens *int64, maxCostUSD *float64) (bool, error) {
	session, err := s.Current(ctx, id)
	if err != nil {
		return false, err
	}
	binding, err := s.BindingForRun(ctx, session.Binding.RunID)
	if err != nil {
		return false, err
	}
	if binding != session.Binding {
		return false, verification.ErrBindingMismatch
	}
	run, err := s.runtime.Execution().GetRun(ctx, binding.RunID)
	if err != nil {
		return false, err
	}
	return s.runtime.store.ReserveGoalCall(ctx, run.SessionID, binding.GoalID, binding.GoalRevision, budget.CallRequest{Key: key, RunID: run.RunID, Process: budget.ProcessVerification, MaxTokens: maxTokens, MaxCostUSD: maxCostUSD})
}
