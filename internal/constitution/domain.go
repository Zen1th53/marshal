package constitution

import (
	"errors"
	"fmt"
)

// ErrInvalidConstitution reports a malformed constitution, registry or
// envelope. It is returned at load or validation time so that a broken
// configuration fails immediately rather than under-enforcing at a gate.
var ErrInvalidConstitution = errors.New("constitution is invalid")

// Domain classifies a material decision. Classification decides which
// invariants apply, so an unclassified action cannot be evaluated and is
// refused rather than allowed by default (Article X).
type Domain string

const (
	// DomainRead covers non-mutating inspection. It is still classified so
	// that project isolation and secret rules apply to reads.
	DomainRead Domain = "read"

	DomainProjectMutation Domain = "project.mutation"
	DomainGoalMutation    Domain = "goal.mutation"
	DomainPlanMutation    Domain = "plan.mutation"
	DomainFileMutation    Domain = "file.mutation"
	DomainShell           Domain = "shell"
	DomainNetwork         Domain = "network"
	DomainCredential      Domain = "credential"
	DomainExternalEffect  Domain = "external.effect"
	DomainApproval        Domain = "approval"
	DomainRouting         Domain = "routing"
	DomainFailover        Domain = "failover"
	DomainCheckpoint      Domain = "checkpoint"
	DomainRollback        Domain = "rollback"
	DomainVerification    Domain = "verification"
	DomainCompletion      Domain = "completion"
	DomainMemoryPromotion Domain = "memory.promotion"
	DomainRecommendation  Domain = "recommendation"
	DomainExport          Domain = "export"
	DomainSessionClose    Domain = "session.close"
	DomainEntitlement     Domain = "entitlement"
	DomainTelemetry       Domain = "telemetry"
)

var allDomains = []Domain{
	DomainRead, DomainProjectMutation, DomainGoalMutation, DomainPlanMutation,
	DomainFileMutation, DomainShell, DomainNetwork, DomainCredential,
	DomainExternalEffect, DomainApproval, DomainRouting, DomainFailover,
	DomainCheckpoint, DomainRollback, DomainVerification, DomainCompletion,
	DomainMemoryPromotion, DomainRecommendation, DomainExport,
	DomainSessionClose, DomainEntitlement, DomainTelemetry,
}

// Valid reports whether the domain is a recognised classification.
func (d Domain) Valid() bool {
	for _, known := range allDomains {
		if d == known {
			return true
		}
	}
	return false
}

// Mutating reports whether the domain can change state outside MARSHAL's own
// records. Mutating domains require a checkpoint or an explicit statement that
// the action is irreversible.
func (d Domain) Mutating() bool {
	switch d {
	case DomainRead, DomainRouting, DomainVerification, DomainTelemetry:
		return false
	default:
		return true
	}
}

// Domains returns every recognised decision domain.
func Domains() []Domain {
	out := make([]Domain, len(allDomains))
	copy(out, allDomains)
	return out
}

// errInvalidf builds an ErrInvalidConstitution-wrapped error.
func errInvalidf(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrInvalidConstitution}, args...)...)
}
