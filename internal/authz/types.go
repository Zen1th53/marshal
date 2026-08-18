package authz

import (
	"context"
	"strings"
	"time"
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
	Allowed   bool            `json:"allowed"`
	Outcome   DecisionOutcome `json:"outcome"`
	Reason    ErrorCode       `json:"reason"`
	SubjectID string          `json:"subject_id"`
	Authority Authority       `json:"authority"`
	Resource  string          `json:"resource"`
	Role      string          `json:"role"`
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
	if !validRoleName(r.Name) {
		return ErrUnknownRole
	}
	if len(r.Authorities) == 0 {
		return ErrRoleInvalid
	}
	seen := make(map[Authority]struct{}, len(r.Authorities))
	for _, authority := range r.Authorities {
		if _, ok := knownAuthorities[authority]; !ok {
			return ErrUnknownAuthority
		}
		if _, duplicate := seen[authority]; duplicate {
			return ErrRoleInvalid
		}
		seen[authority] = struct{}{}
	}
	return nil
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
	decision := Decision{Outcome: OutcomeDeny, Reason: CodeDenied, SubjectID: subject.ID, Authority: authority, Resource: resource, Role: subject.Role.Name}
	if err := ctx.Err(); err != nil {
		return decision, err
	}
	if strings.TrimSpace(subject.ID) == "" {
		decision.Reason = CodeRoleInvalid
		return decision, ErrRoleInvalid
	}
	if err := subject.Role.Validate(); err != nil {
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
