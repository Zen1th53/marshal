package authz

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/capability"
)

type Authority string

const (
	AuthorityTaskPlan       Authority = "task.plan"
	AuthoritySourceWrite    Authority = "source.write"
	AuthorityVerifyQA       Authority = "verify.qa"
	AuthorityVerifySecurity Authority = "verify.security"
	AuthorityReleaseApprove Authority = "release.approve"
	AuthorityReleaseReject  Authority = "release.reject"
	AuthorityPolicyAdmin    Authority = "policy.admin"
)

type Role struct {
	Name         string      `json:"name"`
	Capabilities []string    `json:"capabilities,omitempty"`
	Authorities  []Authority `json:"authorities"`
}

type Principal struct {
	ID   string `json:"id"`
	Role Role   `json:"role"`
}

type RoleBinding struct {
	ID           string       `json:"id"`
	PrincipalID  string       `json:"principal_id"`
	Role         string       `json:"role"`
	ScopeID      string       `json:"scope_id"`
	BoundBy      string       `json:"bound_by"`
	BoundAt      time.Time    `json:"bound_at"`
	RevokedAt    *time.Time   `json:"revoked_at,omitempty"`
	PolicyDigest string       `json:"policy_digest"`
	State        BindingState `json:"state,omitempty"`
}

func (b RoleBinding) Validate() error {
	if strings.TrimSpace(b.ID) == "" || strings.TrimSpace(b.PrincipalID) == "" ||
		strings.TrimSpace(b.Role) == "" || strings.TrimSpace(b.ScopeID) == "" ||
		strings.TrimSpace(b.BoundBy) == "" || b.BoundAt.IsZero() || !validRoleBindingName(b.Role) {
		return ErrRoleInvalid
	}
	if strings.TrimSpace(b.PolicyDigest) == "" || len(b.PolicyDigest) != 71 || !strings.HasPrefix(b.PolicyDigest, "sha256:") {
		return ErrRoleInvalid
	}
	if b.State != "" && b.State != StateUnbound && b.State != StateBound && b.State != StateChanged && b.State != StateRevoked {
		return ErrRoleInvalid
	}
	if b.RevokedAt != nil && !b.RevokedAt.After(b.BoundAt) {
		return ErrRoleInvalid
	}
	return nil
}

type DecisionOutcome string

const (
	OutcomeAllow DecisionOutcome = "ALLOW"
	OutcomeDeny  DecisionOutcome = "DENY"
)

type Decision struct {
	Allowed           bool            `json:"allowed"`
	Outcome           DecisionOutcome `json:"outcome"`
	Reason            ErrorCode       `json:"reason"`
	SubjectID         string          `json:"subject_id"`
	Authority         Authority       `json:"authority"`
	Resource          string          `json:"resource"`
	Role              string          `json:"role"`
	CapabilityGrantID string          `json:"capability_grant_id,omitempty"`
}

var defaultRoles = map[string]struct{}{
	"orchestrator": {}, "architect": {}, "developer": {}, "qa": {}, "appsec": {},
}

var knownAuthorities = map[Authority]struct{}{
	AuthorityTaskPlan: {}, AuthoritySourceWrite: {}, AuthorityVerifyQA: {},
	AuthorityVerifySecurity: {}, AuthorityReleaseApprove: {}, AuthorityReleaseReject: {},
	AuthorityPolicyAdmin: {},
}

func (r Role) Validate() error {
	return validateRole(r, false)
}

func validRoleName(name string) bool {
	_, ok := defaultRoles[strings.ToLower(strings.TrimSpace(name))]
	return ok
}

func validRoleBindingName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 64 {
		return false
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

func Can(ctx context.Context, subject Principal, authority Authority, resource string) (Decision, error) {
	return can(ctx, subject, authority, resource, false)
}

func can(ctx context.Context, subject Principal, authority Authority, resource string, allowCustomRole bool) (Decision, error) {
	decision := Decision{Outcome: OutcomeDeny, Reason: CodeDenied, SubjectID: subject.ID, Authority: authority, Resource: resource, Role: subject.Role.Name}
	if err := ctx.Err(); err != nil {
		return decision, err
	}
	if strings.TrimSpace(subject.ID) == "" {
		decision.Reason = CodeRoleInvalid
		return decision, ErrRoleInvalid
	}
	if err := validateRole(subject.Role, allowCustomRole); err != nil {
		decision.Reason = codeOf(err)
		return decision, err
	}
	if _, ok := knownAuthorities[authority]; !ok {
		decision.Reason = CodeUnknownAuthority
		return decision, ErrUnknownAuthority
	}
	if strings.TrimSpace(resource) == "" {
		decision.Reason = CodeRoleInvalid
		return decision, ErrRoleInvalid
	}
	for _, declared := range subject.Role.Authorities {
		if declared == authority {
			decision.Allowed = true
			decision.Outcome = OutcomeAllow
			decision.Reason = CodeAllowed
			return decision, nil
		}
	}
	return decision, ErrDenied
}

// CanWithCapability composes role authority with the canonical T01 concrete
// capability check. A role declaration never substitutes for a scoped grant.
func CanWithCapability(ctx context.Context, subject Principal, authority Authority, resource string, query capability.Query, broker capability.Broker) (Decision, error) {
	decision, err := Can(ctx, subject, authority, resource)
	if err != nil {
		return decision, err
	}
	if broker == nil || query.Subject != capability.SubjectID(subject.ID) || query.Resource != resource {
		decision.Allowed = false
		decision.Outcome = OutcomeDeny
		decision.Reason = CodeDenied
		return decision, ErrDenied
	}
	capabilityDecision, err := broker.Authorize(ctx, query)
	if err != nil || capabilityDecision.Outcome != capability.OutcomeAllow {
		decision.Allowed = false
		decision.Outcome = OutcomeDeny
		decision.Reason = CodeDenied
		if err != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
			return decision, err
		}
		return decision, ErrDenied
	}
	decision.CapabilityGrantID = string(capabilityDecision.MatchedGrant)
	return decision, nil
}
