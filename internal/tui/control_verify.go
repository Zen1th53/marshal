package tui

import (
	"context"
	"fmt"
	"github.com/Zen1th53/marshal/internal/app"
	"time"
)

type VerificationActionAuthority interface {
	ReviewVerificationAction(context.Context, string) (app.VerificationActionReview, error)
	ApplyVerificationAction(context.Context, app.CommandEnvelope, app.VerificationActionRequest) (app.VerificationActionResult, error)
}

func verificationActionBindings(s *ControlSource) map[ActionID]Binding {
	out := map[ActionID]Binding{}
	for id, spec := range map[ActionID]struct {
		op, title string
		safety    Safety
	}{
		"CTUI-0352": {"challenge", "Challenge with counter-evidence", SafetyGoverned},
		"CTUI-0382": {"close-finding", "Authorized finding closure", SafetyGoverned},
		"CTUI-0395": {"attest", "Completion attestation", SafetyGoverned},
		"CTUI-0399": {"build-bundle", "Build bundle", SafetyPlain},
		"CTUI-0402": {"export-bundle", "Export bundle", SafetyPlain},
	} {
		id, op, title := id, spec.op, spec.title
		inputs := []InputSpec{{Key: "verification_id", Label: "Exact verification session ID", Required: true}}
		switch op {
		case "challenge":
			inputs = append(inputs, InputSpec{Key: "claim_id", Label: "Claim ID", Required: true}, InputSpec{Key: "evidence_id", Label: "Canonical counter-evidence ID", Required: true}, InputSpec{Key: "reason", Label: "Challenge reason", Required: true})
		case "close-finding":
			inputs = append(inputs, InputSpec{Key: "finding_id", Label: "Exact finding ID", Required: true}, InputSpec{Key: "reason", Label: "Closure reason", Required: true}, InputSpec{Key: "expires_at", Label: "Waiver expiry (RFC3339, within 24h)", Required: true, Validate: validateWaiverExpiry})
		case "export-bundle":
			inputs = append(inputs, InputSpec{Key: "path", Label: "New project-relative export file", Required: true})
		}
		b := Binding{Action: id, Title: title, Safety: spec.safety, Inputs: inputs, PrepareRequest: func(ctx context.Context, req ActionRequest) (Target, error) {
			a, ok := s.Authority.(VerificationActionAuthority)
			if !ok {
				return Target{}, ErrNoBinding
			}
			review, err := a.ReviewVerificationAction(ctx, req.Inputs["verification_id"])
			if err != nil {
				return Target{}, err
			}
			return Target{Kind: "verification", ID: review.Session.ID, Revision: review.Session.Version, Digest: review.Digest, Scope: fmt.Sprintf("run %s; tree #%s", review.Session.Binding.RunID, shortDigest(review.Session.Binding.TreeDigest)), Summary: fmt.Sprintf("%s %s @%d; bundle #%s (%d bytes)", op, review.Session.ID, review.Session.Version, shortDigest(review.BundleDigest), review.ExportSize)}, nil
		}, Execute: func(ctx context.Context, req ActionRequest) (Outcome, error) {
			a, ok := s.Authority.(VerificationActionAuthority)
			if !ok {
				return Outcome{}, ErrNoBinding
			}
			review, err := a.ReviewVerificationAction(ctx, req.Target.ID)
			if err != nil {
				return Outcome{}, err
			}
			if review.Digest != req.Target.Digest || review.Session.Version != req.Target.Revision {
				return refusal("verification changed", ErrStaleTarget, req.Target, "Runtime.CommandVerification")
			}
			input := app.VerificationActionRequest{Operation: op, ExpectedDigest: req.Target.Digest, ClaimID: req.Inputs["claim_id"], EvidenceID: req.Inputs["evidence_id"], FindingID: req.Inputs["finding_id"], Reason: req.Inputs["reason"], Destination: req.Inputs["path"], BundleDigest: review.BundleDigest}
			if op == "close-finding" {
				input.ExpiresAt, err = time.Parse(time.RFC3339, req.Inputs["expires_at"])
				if err != nil {
					return Outcome{}, err
				}
			}
			result, err := a.ApplyVerificationAction(ctx, memoryEnvelope(s, req), input)
			if err != nil {
				return refusal("verification "+op, err, req.Target, "Runtime.CommandVerification")
			}
			detail := fmt.Sprintf("Verification %s @%d: %s", result.Session.ID, result.Session.Version, result.Session.State)
			proof := memoryBindingDigest(result.Session)
			if result.Bundle != nil {
				detail = fmt.Sprintf("Evidence metadata bundle reread at %s: %d bytes #%s; completion is evaluated separately", result.Bundle.Path, result.Bundle.Size, shortDigest(result.Bundle.Digest))
				proof = result.Bundle.Digest
			}
			if result.Attestation != nil {
				detail = fmt.Sprintf("Attestation %s: %s at verification revision %d", result.Attestation.ID, result.Attestation.Decision, result.Attestation.VerificationVersion)
				proof = result.Attestation.Digest
			}
			return memorySuccess(req.Target, detail, proof, "Runtime.CommandVerification"), nil
		}}
		if _, ok := s.Authority.(VerificationActionAuthority); !ok {
			b.Requires = "authenticated local verification action authority is unavailable"
			b.Execute = nil
		}
		out[id] = b
	}
	return out
}
func (a *runtimeControlAuthority) ReviewVerificationAction(ctx context.Context, id string) (app.VerificationActionReview, error) {
	ctx, err := a.memoryActionContext(ctx)
	if err != nil {
		return app.VerificationActionReview{}, err
	}
	return a.runtime.ReviewVerificationAction(ctx, id)
}
func (a *runtimeControlAuthority) ApplyVerificationAction(ctx context.Context, e app.CommandEnvelope, req app.VerificationActionRequest) (app.VerificationActionResult, error) {
	ctx, err := a.memoryActionContext(ctx)
	if err != nil {
		return app.VerificationActionResult{}, err
	}
	return a.runtime.CommandVerification(ctx, e, req)
}

// validateWaiverExpiry refuses an expiry that is not an RFC 3339 time in
// words the operator can act on; time.Parse's own error quotes Go's layout
// string ("2006-01-02T15:04:05Z07:00"), which reads as nonsense in a form.
func validateWaiverExpiry(v string) error {
	if _, err := time.Parse(time.RFC3339, v); err != nil {
		return fmt.Errorf("waiver expiry must be an RFC 3339 time, for example %s", time.Now().UTC().Add(12*time.Hour).Truncate(time.Hour).Format(time.RFC3339))
	}
	return nil
}
