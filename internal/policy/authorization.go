package policy

import (
	"context"
	"time"
)

// ManagementAction identifies one exact policy-management lifecycle edge.
// Provider and model labels are deliberately absent: they never confer
// management authority.
type ManagementAction string

const (
	ActionPolicyValidate  ManagementAction = "policy.validate"
	ActionPolicyActivate  ManagementAction = "policy.activate"
	ActionPolicySupersede ManagementAction = "policy.supersede"
)

func (a ManagementAction) Valid() bool {
	switch a {
	case ActionPolicyValidate, ActionPolicyActivate, ActionPolicySupersede:
		return true
	default:
		return false
	}
}

func actionForTransition(from, to State) (ManagementAction, bool) {
	switch {
	case from == StateLoaded && to == StateValidated:
		return ActionPolicyValidate, true
	case from == StateValidated && to == StateActive:
		return ActionPolicyActivate, true
	case from == StateActive && to == StateSuperseded:
		return ActionPolicySupersede, true
	default:
		return "", false
	}
}

// PolicyMutationRequest binds authorization to one canonical lifecycle edge.
// Identity fields must come from authenticated runtime context; callers may
// not turn policy attributes or provider labels into authority.
type PolicyMutationRequest struct {
	SubjectID     string
	SessionID     string
	TaskID        string
	ChangeID      string
	PolicyID      PolicyID
	PolicyVersion PolicyVersion
	ExpectedState State
	TargetState   State
	Binding       PolicyBinding
	Action        ManagementAction
}

// Validate enforces bounded, fail-closed request semantics before an
// authorizer is consulted.
func (r PolicyMutationRequest) Validate() error {
	if !validIdentifier(r.SubjectID) || !validIdentifier(r.SessionID) ||
		!validIdentifier(r.TaskID) || !validIdentifier(r.ChangeID) ||
		!validIdentifier(string(r.PolicyID)) || r.PolicyVersion <= 0 ||
		!r.ExpectedState.Valid() || !r.TargetState.Valid() ||
		!CanTransition(r.ExpectedState, r.TargetState) || !r.Action.Valid() {
		return NewError(CodeAuthorizationInvalid, nil)
	}
	bindingErr := r.Binding.Validate()
	if bindingErr != nil || r.Binding.Version != r.PolicyVersion {
		return NewError(CodeAuthorizationInvalid, nil)
	}
	expectedAction, ok := actionForTransition(r.ExpectedState, r.TargetState)
	if !ok || expectedAction != r.Action {
		return NewError(CodeAuthorizationInvalid, nil)
	}
	return nil
}

// PolicyMutationDecision is an untrusted authorizer result. It becomes
// actionable only after ValidateFor proves exact operation and freshness
// binding; an allowed bit alone is never authority.
type PolicyMutationDecision struct {
	Allowed       bool
	SubjectID     string
	SessionID     string
	TaskID        string
	ChangeID      string
	PolicyID      PolicyID
	PolicyVersion PolicyVersion
	ExpectedState State
	TargetState   State
	Binding       PolicyBinding
	Action        ManagementAction
	ExpiresAt     time.Time
}

// ValidateFor rejects deny, malformed, expired, stale, or replayed decisions.
func (d PolicyMutationDecision) ValidateFor(request PolicyMutationRequest) error {
	if err := request.Validate(); err != nil {
		return NewError(CodeAuthorizationInvalid, nil)
	}
	if !d.Allowed {
		return NewError(CodeAuthorizationDenied, nil)
	}
	if !validIdentifier(d.SubjectID) || !validIdentifier(d.SessionID) ||
		!validIdentifier(d.TaskID) || !validIdentifier(d.ChangeID) ||
		d.PolicyID != request.PolicyID || d.PolicyVersion != request.PolicyVersion ||
		d.ExpectedState != request.ExpectedState || d.TargetState != request.TargetState ||
		d.Action != request.Action || d.SubjectID != request.SubjectID ||
		d.SessionID != request.SessionID || d.TaskID != request.TaskID ||
		d.ChangeID != request.ChangeID {
		return NewError(CodeAuthorizationStale, nil)
	}
	if err := d.Binding.Validate(); err != nil || !d.Binding.FreshAgainst(request.Binding) {
		return NewError(CodeAuthorizationStale, nil)
	}
	if !d.ExpiresAt.IsZero() && !time.Now().UTC().Before(d.ExpiresAt.UTC()) {
		return NewError(CodeAuthorizationStale, nil)
	}
	return nil
}

// ManagementAuthorizer is the narrow dependency-inversion seam for the
// future canonical authority implementation. It must not evaluate target
// policy content as a self-granting management authority.
type ManagementAuthorizer interface {
	AuthorizePolicyMutation(context.Context, PolicyMutationRequest) (PolicyMutationDecision, error)
}
