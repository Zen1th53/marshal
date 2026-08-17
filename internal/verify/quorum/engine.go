package quorum

import (
	"context"
	"strings"
	"time"
)

type Engine struct {
	now func() time.Time
}

func NewEngine(now func() time.Time) *Engine {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Engine{now: now}
}

func (e *Engine) Evaluate(ctx context.Context, requirements []Requirement, attestations []Attestation, provenance Provenance) (Evaluation, error) {
	if err := ctx.Err(); err != nil {
		return Evaluation{State: StateInvalidated}, err
	}
	if err := provenance.Validate(); err != nil {
		return Evaluation{State: StateInvalidated}, err
	}
	for _, requirement := range requirements {
		if err := requirement.Validate(); err != nil {
			return Evaluation{State: StateInvalidated}, err
		}
	}
	result := Evaluation{State: StatePending}
	seen := make(map[string]struct{}, len(attestations))
	for _, attestation := range attestations {
		if err := attestation.Validate(); err != nil {
			return Evaluation{State: StateInvalidated}, err
		}
		if attestation.ChangeID != provenance.ChangeID || attestation.ContentDigest != provenance.ContentDigest {
			return Evaluation{State: StateInvalidated}, ErrStaleAttestation
		}
		if attestation.InvalidatedAt != nil || !attestation.CreatedAt.Before(e.now().Add(time.Nanosecond)) {
			return Evaluation{State: StateInvalidated}, ErrStaleAttestation
		}
		if attestation.Result == ResultVeto {
			result.Rejected = append(result.Rejected, attestation)
			result.State = StateBlocked
			return result, ErrVeto
		}
		key := strings.TrimSpace(attestation.Subject) + "\x00" + string(attestation.Kind)
		if _, exists := seen[key]; exists {
			return Evaluation{State: StateInvalidated}, ErrDuplicatePrincipal
		}
		seen[key] = struct{}{}
		result.Accepted = append(result.Accepted, attestation)
	}
	for _, requirement := range requirements {
		count := 0
		for _, attestation := range result.Accepted {
			if attestation.Kind != requirement.Kind || attestation.Result != ResultPass || !matches(requirement, attestation) {
				continue
			}
			count++
		}
		if count < requirement.Minimum {
			result.Missing = append(result.Missing, requirement)
		}
	}
	if len(result.Missing) == 0 {
		result.State = StateSatisfied
		result.Satisfied = true
	} else if len(result.Accepted) > 0 {
		result.State = StatePartiallySatisfied
	}
	return result, nil
}

func matches(requirement Requirement, attestation Attestation) bool {
	roleOK := len(requirement.AllowedRoles) == 0 || contains(requirement.AllowedRoles, attestation.Role)
	providerOK := len(requirement.AllowedProviders) == 0 || contains(requirement.AllowedProviders, attestation.Provider)
	return roleOK && providerOK
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
