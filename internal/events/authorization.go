package events

import (
	"context"
	"regexp"
	"time"
)

// ProducerIdentity is obtained from the authenticated runtime boundary. Event
// payload fields never manufacture this identity.
type ProducerIdentity struct {
	SubjectID SubjectID
	SessionID string
	TaskID    TaskID
	RunID     RunID
	ChangeID  string
}

func (i ProducerIdentity) valid() bool {
	if !validRequired(string(i.SubjectID)) || !validRequired(i.SessionID) {
		return false
	}
	for _, value := range []string{string(i.TaskID), string(i.RunID), i.ChangeID} {
		if value != "" && !validOptional(value) {
			return false
		}
	}
	return true
}

type ProducerAction string

const ActionAppend ProducerAction = "events.append"

// AuthorizationRequest binds an append decision to the immutable event
// identity and all security-relevant foreign references.
type AuthorizationRequest struct {
	Identity       ProducerIdentity
	Action         ProducerAction
	EventID        EventID
	Type           Type
	TaskID         TaskID
	RunID          RunID
	ResourceID     ResourceID
	EvidenceID     EvidenceID
	IdempotencyKey IdempotencyKey
}

func (r AuthorizationRequest) valid() bool {
	return r.Identity.valid() && r.Action == ActionAppend &&
		validRequired(string(r.EventID)) && r.Type.Valid() &&
		validRequired(string(r.IdempotencyKey))
}

// AuthorizationDecision is untrusted until exact binding and canonical
// freshness validation both succeed.
type AuthorizationDecision struct {
	Allowed        bool
	Identity       ProducerIdentity
	Action         ProducerAction
	EventID        EventID
	Type           Type
	TaskID         TaskID
	RunID          RunID
	ResourceID     ResourceID
	EvidenceID     EvidenceID
	IdempotencyKey IdempotencyKey
	PolicyDigest   string
	FreshUntil     time.Time
}

type IdentityProvider interface {
	Identity(context.Context) (ProducerIdentity, error)
}

type Authorizer interface {
	Authorize(context.Context, AuthorizationRequest) (AuthorizationDecision, error)
}

type FreshnessValidator interface {
	ValidateFreshness(context.Context, AuthorizationRequest, AuthorizationDecision) error
}

type IdentityProviderFunc func(context.Context) (ProducerIdentity, error)

func (f IdentityProviderFunc) Identity(ctx context.Context) (ProducerIdentity, error) { return f(ctx) }

type AuthorizerFunc func(context.Context, AuthorizationRequest) (AuthorizationDecision, error)

func (f AuthorizerFunc) Authorize(ctx context.Context, r AuthorizationRequest) (AuthorizationDecision, error) {
	return f(ctx, r)
}

type FreshnessValidatorFunc func(context.Context, AuthorizationRequest, AuthorizationDecision) error

func (f FreshnessValidatorFunc) ValidateFreshness(ctx context.Context, r AuthorizationRequest, d AuthorizationDecision) error {
	return f(ctx, r, d)
}

var authorizationDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func authorizationRequestFor(identity ProducerIdentity, event Event) AuthorizationRequest {
	return AuthorizationRequest{
		Identity: identity, Action: ActionAppend, EventID: event.ID, Type: event.Type,
		TaskID: event.TaskID, RunID: event.RunID, ResourceID: event.ResourceID,
		EvidenceID: event.EvidenceID, IdempotencyKey: event.IdempotencyKey,
	}
}

func (d AuthorizationDecision) validateFor(r AuthorizationRequest, now time.Time) error {
	if !d.Allowed {
		return ErrAuthorizationDenied
	}
	if d.Identity != r.Identity || d.Action != r.Action || d.EventID != r.EventID ||
		d.Type != r.Type || d.TaskID != r.TaskID || d.RunID != r.RunID ||
		d.ResourceID != r.ResourceID || d.EvidenceID != r.EvidenceID ||
		d.IdempotencyKey != r.IdempotencyKey {
		return ErrAuthorizationDenied
	}
	if !authorizationDigestPattern.MatchString(d.PolicyDigest) || d.FreshUntil.IsZero() || !d.FreshUntil.After(now) {
		return ErrAuthorizationStale
	}
	return nil
}
