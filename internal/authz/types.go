package authz

import (
	"context"
	"strings"
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
	if _, ok := defaultRoles[strings.ToLower(strings.TrimSpace(r.Name))]; !ok {
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
