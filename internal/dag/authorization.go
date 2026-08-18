package dag

import (
	"context"
	"regexp"
	"time"
)

// MutationAction is the closed privileged DAG mutation vocabulary. Provider
// or model labels are deliberately absent: they never confer authority.
type MutationAction string

const (
	ActionAddNode    MutationAction = "dag.node.add"
	ActionAddEdge    MutationAction = "dag.edge.add"
	ActionTransition MutationAction = "dag.node.transition"
)

func (a MutationAction) Valid() bool {
	switch a {
	case ActionAddNode, ActionAddEdge, ActionTransition:
		return true
	default:
		return false
	}
}

// Identity is supplied by the authenticated runtime boundary, not by DAG
// payloads or agent prose.
type Identity struct {
	SubjectID string
	SessionID string
	TaskID    string
	ChangeID  string
}

func (i Identity) valid() bool {
	return validAuthorityID(i.SubjectID) && validAuthorityID(i.SessionID) &&
		validAuthorityID(i.TaskID) && validAuthorityID(i.ChangeID)
}

// MutationResource is a typed normalized resource. Exactly one of Node or the
// Parent/Child edge pair is populated for an operation.
type MutationResource struct {
	Node   TaskID
	Parent TaskID
	Child  TaskID
}

func nodeResource(id TaskID) MutationResource { return MutationResource{Node: id} }
func edgeResource(edge Edge) MutationResource {
	return MutationResource{Parent: edge.From, Child: edge.To}
}

func (r MutationResource) valid() bool {
	if r.Node != "" {
		return validTaskID(r.Node) && r.Parent == "" && r.Child == ""
	}
	return r.Node == "" && validTaskID(r.Parent) && validTaskID(r.Child) && r.Parent != r.Child
}

// AuthorizationRequest binds authority to one exact DAG side effect. State
// fields are populated only for lifecycle transitions.
type AuthorizationRequest struct {
	Identity      Identity
	RequestID     RequestID
	Action        MutationAction
	Resource      MutationResource
	ExpectedState NodeStatus
	TargetState   NodeStatus
}

func (r AuthorizationRequest) valid() bool {
	if !r.Identity.valid() || !r.Action.Valid() || !r.Resource.valid() {
		return false
	}
	switch r.Action {
	case ActionAddNode, ActionAddEdge:
		return validRequestID(r.RequestID) && r.ExpectedState == "" && r.TargetState == ""
	case ActionTransition:
		return r.RequestID == "" && CanTransition(r.ExpectedState, r.TargetState)
	default:
		return false
	}
}

// AuthorizationDecision is untrusted until it exactly matches the request and
// has a current policy digest and freshness deadline.
type AuthorizationDecision struct {
	Allowed       bool
	Identity      Identity
	RequestID     RequestID
	Action        MutationAction
	Resource      MutationResource
	ExpectedState NodeStatus
	TargetState   NodeStatus
	PolicyDigest  string
	FreshUntil    time.Time
}

type IdentityProvider interface {
	Identity(context.Context) (Identity, error)
}

type Authorizer interface {
	Authorize(context.Context, AuthorizationRequest) (AuthorizationDecision, error)
}

// FreshnessValidator bridges to the canonical policy/authority owner. A
// previously allowed decision is not reusable merely because its deadline has
// not elapsed; the current authority generation/digest may have changed.
type FreshnessValidator interface {
	ValidateFreshness(context.Context, AuthorizationRequest, AuthorizationDecision) error
}

type IdentityProviderFunc func(context.Context) (Identity, error)

func (f IdentityProviderFunc) Identity(ctx context.Context) (Identity, error) { return f(ctx) }

type AuthorizerFunc func(context.Context, AuthorizationRequest) (AuthorizationDecision, error)

func (f AuthorizerFunc) Authorize(ctx context.Context, r AuthorizationRequest) (AuthorizationDecision, error) {
	return f(ctx, r)
}

type FreshnessValidatorFunc func(context.Context, AuthorizationRequest, AuthorizationDecision) error

func (f FreshnessValidatorFunc) ValidateFreshness(ctx context.Context, r AuthorizationRequest, d AuthorizationDecision) error {
	return f(ctx, r, d)
}

var policyDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func (d AuthorizationDecision) validateFor(request AuthorizationRequest, now time.Time) error {
	if !d.Allowed {
		return ErrAuthorizationDenied
	}
	if d.Identity != request.Identity || d.RequestID != request.RequestID ||
		d.Action != request.Action || d.Resource != request.Resource ||
		d.ExpectedState != request.ExpectedState || d.TargetState != request.TargetState {
		return ErrAuthorizationDenied
	}
	if !policyDigestPattern.MatchString(d.PolicyDigest) || d.FreshUntil.IsZero() || !d.FreshUntil.After(now) {
		return ErrAuthorizationStale
	}
	return nil
}

func validAuthorityID(value string) bool {
	return value != "" && len(value) <= maxIdentifierBytes && validText(value)
}
