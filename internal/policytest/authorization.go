package policytest

import (
	"context"
	"time"

	"github.com/Zen1th53/marshal/internal/policy"
)

// Action identifies the sole A04-protected T49 lifecycle operation. The exact
// state edge remains part of the request and is validated independently.
type Action string

const ActionTransition Action = "policytest.transition"

func (a Action) Valid() bool { return a == ActionTransition }

// AuthorizationRequest binds authority to one exact persisted run, policy
// target, lifecycle edge, and authenticated execution identity.
type AuthorizationRequest struct {
	SubjectID      string
	SessionID      string
	TaskID         string
	ChangeID       string
	RunID          string
	PolicyID       policy.PolicyID
	Binding        policy.PolicyBinding
	TestFileDigest policy.PolicyDigest
	ExpectedState  RunState
	TargetState    RunState
	Action         Action
}

func (r AuthorizationRequest) Validate() error {
	if !validBoundedID(r.SubjectID, maxSuiteID) || !validBoundedID(r.SessionID, maxSuiteID) ||
		!validBoundedID(r.TaskID, maxSuiteID) || !validBoundedID(r.ChangeID, maxSuiteID) ||
		!validBoundedID(r.RunID, maxSuiteID) || !validBoundedID(string(r.PolicyID), maxSuiteID) ||
		!r.Action.Valid() || !r.ExpectedState.Valid() || !r.TargetState.Valid() {
		return policy.ErrAuthorizationInvalid
	}
	if err := r.Binding.Validate(); err != nil || r.TestFileDigest.Validate() != nil {
		return policy.ErrAuthorizationInvalid
	}
	if err := ValidateTransition(r.ExpectedState, r.TargetState); err != nil {
		return policy.ErrAuthorizationInvalid
	}
	return nil
}

// AuthorizationDecision is untrusted authorizer output. Allowed alone is
// never sufficient; ValidateFor requires an exact echo of every binding fact.
type AuthorizationDecision struct {
	Allowed        bool
	SubjectID      string
	SessionID      string
	TaskID         string
	ChangeID       string
	RunID          string
	PolicyID       policy.PolicyID
	Binding        policy.PolicyBinding
	TestFileDigest policy.PolicyDigest
	ExpectedState  RunState
	TargetState    RunState
	Action         Action
	FreshUntil     time.Time
}

func (d AuthorizationDecision) ValidateFor(request AuthorizationRequest) error {
	if err := request.Validate(); err != nil {
		return policy.ErrAuthorizationInvalid
	}
	if !d.Allowed {
		return policy.ErrAuthorizationDenied
	}
	if !validBoundedID(d.SubjectID, maxSuiteID) || !validBoundedID(d.SessionID, maxSuiteID) ||
		!validBoundedID(d.TaskID, maxSuiteID) || !validBoundedID(d.ChangeID, maxSuiteID) ||
		!validBoundedID(d.RunID, maxSuiteID) || d.PolicyID != request.PolicyID ||
		d.RunID != request.RunID || d.SubjectID != request.SubjectID || d.SessionID != request.SessionID ||
		d.TaskID != request.TaskID || d.ChangeID != request.ChangeID || d.ExpectedState != request.ExpectedState ||
		d.TargetState != request.TargetState || d.Action != request.Action ||
		d.TestFileDigest != request.TestFileDigest {
		return policy.ErrAuthorizationStale
	}
	if err := d.Binding.Validate(); err != nil || !d.Binding.FreshAgainst(request.Binding) {
		return policy.ErrAuthorizationStale
	}
	if !d.FreshUntil.IsZero() && !time.Now().UTC().Before(d.FreshUntil.UTC()) {
		return policy.ErrAuthorizationStale
	}
	return nil
}

// Authorizer is the narrow trusted authority seam. Fixture content, result
// status, and provider labels are never consulted as authority.
type Authorizer interface {
	AuthorizePolicyTestRun(context.Context, AuthorizationRequest) (AuthorizationDecision, error)
}
