package epistemic

import (
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// ClaimFormationDiscipline enforces Discipline A: Claim Formation.
// It defends against wrong premises, hidden assumptions, uncertainty presented as fact,
// and premature certainty over monolithic/over-broad claims.
type ClaimFormationDiscipline struct{}

func NewClaimFormationDiscipline() *ClaimFormationDiscipline {
	return &ClaimFormationDiscipline{}
}

var hedgeWords = []string{
	"likely", "probably", "might", "seems", "presumed", "assumed",
	"believed", "in theory", "should work", "appears to", "supposedly",
}

var overbroadPatterns = []string{
	"auth is secure",
	"all auth secure",
	"authentication is secure",
	"system is bug-free",
	"no bugs exist",
	"everything works",
	"entire codebase is secure",
	"full security verified",
	"100% compliant",
}

// IsOverbroad checks if a claim is a monolithic assertion that hides specific failure modes.
func (d *ClaimFormationDiscipline) IsOverbroad(claim model.Claim) (bool, string) {
	normSubj := strings.ToLower(claim.Subject)
	normText := strings.ToLower(claim.NormalizedText)

	for _, p := range overbroadPatterns {
		if strings.Contains(normSubj, p) || strings.Contains(normText, p) {
			return true, fmt.Sprintf("claim matches monolithic pattern %q; must be decomposed into scoped claims", p)
		}
	}

	// Global un-scoped claims
	if (claim.Scope == "*" || claim.Scope == "all" || claim.Scope == "global") && claim.Criticality.IsCritical() {
		return true, "critical claim with wild-card scope is over-broad; must specify a concrete scope"
	}

	return false, ""
}

// Decompose breaks down a known over-broad claim into concrete scoped sub-claims.
func (d *ClaimFormationDiscipline) Decompose(claim model.Claim) ([]model.Claim, error) {
	normSubj := strings.ToLower(claim.Subject)
	normText := strings.ToLower(claim.NormalizedText)

	var subScopes []struct {
		SubScope string
		Subject  string
		Text     string
	}

	if strings.Contains(normSubj, "auth") || strings.Contains(normText, "auth") {
		subScopes = []struct {
			SubScope string
			Subject  string
			Text     string
		}{
			{"auth.tokens", "auth.token_signature_verification", "Cryptographic token signatures are validated with non-malleable keys"},
			{"auth.rate_limiting", "auth.login_rate_limiting", "Authentication endpoints enforce rate limits against credential stuffing"},
			{"auth.replay", "auth.replay_prevention", "Authentication tokens incorporate nonces/timestamps preventing replay attacks"},
			{"auth.rbac", "auth.role_authorization", "Role bindings restrict access to authorized scopes and actions"},
		}
	} else {
		// Generic decomposition for other over-broad claims
		subScopes = []struct {
			SubScope string
			Subject  string
			Text     string
		}{
			{claim.Scope + ".interface", claim.Subject + ".interface", "Interface boundaries and inputs are validated"},
			{claim.Scope + ".logic", claim.Subject + ".logic", "Core state transitions conform to deterministic contracts"},
			{claim.Scope + ".isolation", claim.Subject + ".isolation", "Execution maintains isolation invariants"},
		}
	}

	decomposed := make([]model.Claim, 0, len(subScopes))
	now := time.Now().UTC()
	for i, s := range subScopes {
		subClaim := model.Claim{
			ID:             fmt.Sprintf("%s-SUB-%d", claim.ID, i+1),
			GoalID:         claim.GoalID,
			GoalRevision:   claim.GoalRevision,
			Subject:        s.Subject,
			NormalizedText: s.Text,
			Scope:          s.SubScope,
			Criticality:    claim.Criticality,
			State:          model.ClaimStateUnsupported, // Start as unsupported, requiring empirical verification
			PredecessorID:  claim.ID,
			Author:         claim.Author,
			Binding:        claim.Binding,
			StateReason:    fmt.Sprintf("Decomposed from over-broad claim %s (%s)", claim.ID, claim.Subject),
			CreatedAt:      now,
			UpdatedAt:      now,
			EvaluatedAt:    now,
		}
		decomposed = append(decomposed, subClaim)
	}

	return decomposed, nil
}

// ValidateFormation inspects a proposed claim to ensure formation discipline.
func (d *ClaimFormationDiscipline) ValidateFormation(claim model.Claim) error {
	if err := claim.Validate(); err != nil {
		return err
	}

	// Over-broad claims cannot be directly VERIFIED
	if claim.State == model.ClaimStateVerified {
		if overbroad, reason := d.IsOverbroad(claim); overbroad {
			return fmt.Errorf("%w: %s", ErrOverbroadClaim, reason)
		}
	}

	// Check for uncertainty presented as fact
	normText := strings.ToLower(claim.NormalizedText)
	if claim.State == model.ClaimStateVerified {
		for _, hw := range hedgeWords {
			if strings.Contains(normText, hw) {
				return fmt.Errorf("%w: text contains hedge word %q", ErrUncertaintyPresentedAsFact, hw)
			}
		}
	}

	return nil
}
