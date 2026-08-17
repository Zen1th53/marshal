// Package events defines MARSHAL's provider-neutral structured event contract.
// Persistence and delivery implementations must keep durable storage as the
// source of truth; this package intentionally contains only the contract.
package events

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const maxDataBytes = 64 * 1024

// Sequence is the store-assigned, strictly increasing event position.
type Sequence uint64

// LifecycleState is the explicit state of an event through the durable
// delivery pipeline. State is not inferred from field presence.
type LifecycleState string

const (
	StateProduced        LifecycleState = "produced"
	StateValidated       LifecycleState = "validated"
	StateDurablyAppended LifecycleState = "durably_appended"
	StatePublished       LifecycleState = "published"
	StateConsumed        LifecycleState = "consumed"
)

// ValidateTransition enforces the only legal forward lifecycle edges.
func ValidateTransition(source, target LifecycleState) error {
	switch {
	case source == StateProduced && target == StateValidated,
		source == StateValidated && target == StateDurablyAppended,
		source == StateDurablyAppended && target == StatePublished,
		source == StatePublished && target == StateConsumed:
		return nil
	default:
		return ErrEventIllegalTransition
	}
}

// EventType is a closed, provider-neutral lifecycle vocabulary.
type EventType string

const (
	EventTypeAgentCreated       EventType = "agent.created"
	EventTypeAgentStarted       EventType = "agent.started"
	EventTypeAgentCompleted     EventType = "agent.completed"
	EventTypeAgentFailed        EventType = "agent.failed"
	EventTypeTaskCreated        EventType = "task.created"
	EventTypeTaskClaimed        EventType = "task.claimed"
	EventTypeTaskCompleted      EventType = "task.completed"
	EventTypeTaskFailed         EventType = "task.failed"
	EventTypeToolStarted        EventType = "tool.started"
	EventTypeToolCompleted      EventType = "tool.completed"
	EventTypeToolFailed         EventType = "tool.failed"
	EventTypePolicyAllowed      EventType = "policy.allowed"
	EventTypePolicyDenied       EventType = "policy.denied"
	EventTypeFileChanged        EventType = "file.changed"
	EventTypeTestStarted        EventType = "test.started"
	EventTypeTestPassed         EventType = "test.passed"
	EventTypeTestFailed         EventType = "test.failed"
	EventTypeVerificationPassed EventType = "verification.passed"
	EventTypeVerificationFailed EventType = "verification.failed"
	EventTypeApprovalRequested  EventType = "approval.requested"
	EventTypeApprovalGranted    EventType = "approval.granted"
	EventTypeApprovalDenied     EventType = "approval.denied"

	EventTypeAppended          EventType = "events.appended"
	EventTypeSubscriberDropped EventType = "events.subscriber.dropped"
	EventTypeSchemaRejected    EventType = "events.schema.rejected"
)

var eventTypes = map[EventType]struct{}{
	EventTypeAgentCreated: {}, EventTypeAgentStarted: {}, EventTypeAgentCompleted: {}, EventTypeAgentFailed: {},
	EventTypeTaskCreated: {}, EventTypeTaskClaimed: {}, EventTypeTaskCompleted: {}, EventTypeTaskFailed: {},
	EventTypeToolStarted: {}, EventTypeToolCompleted: {}, EventTypeToolFailed: {},
	EventTypePolicyAllowed: {}, EventTypePolicyDenied: {}, EventTypeFileChanged: {},
	EventTypeTestStarted: {}, EventTypeTestPassed: {}, EventTypeTestFailed: {},
	EventTypeVerificationPassed: {}, EventTypeVerificationFailed: {},
	EventTypeApprovalRequested: {}, EventTypeApprovalGranted: {}, EventTypeApprovalDenied: {},
	EventTypeAppended: {}, EventTypeSubscriberDropped: {}, EventTypeSchemaRejected: {},
}

// Valid reports whether the type is in the canonical registry.
func (t EventType) Valid() bool { _, ok := eventTypes[t]; return ok }

// Event is the immutable semantic record exchanged by producers, stores and
// subscribers. Sequence is assigned by Store.Append; producers must not forge
// it. Data must contain bounded, non-sensitive references rather than payloads.
type Event struct {
	ID             string         `json:"event_id"`
	Sequence       Sequence       `json:"sequence"`
	Type           EventType      `json:"event_type"`
	Subject        string         `json:"subject,omitempty"`
	TaskID         string         `json:"task_id,omitempty"`
	RunID          string         `json:"run_id,omitempty"`
	ResourceID     string         `json:"resource_id,omitempty"`
	EvidenceID     string         `json:"evidence_id,omitempty"`
	At             time.Time      `json:"at"`
	Data           map[string]any `json:"data,omitempty"`
	IdempotencyKey string         `json:"idempotency_key,omitempty"`
}

// AuthorizationDecision is the provider-neutral, fail-closed result of the
// owning policy/capability boundary. FreshUntil is authoritative for replay.
type AuthorizationDecision struct {
	Allowed    bool
	ReasonCode Code
	FreshUntil time.Time
}

// Authorizer owns policy and authority evaluation; events only consume its
// structured decision and never parse provider-specific messages.
type Authorizer interface {
	Authorize(context.Context, Event) (AuthorizationDecision, error)
}

// Validate checks the contract boundary before a store or bus can observe an
// event. Secret-bearing field names are rejected rather than silently stored.
func (e Event) Validate() error {
	if !e.Type.Valid() {
		return ErrEventTypeInvalid
	}
	for key := range e.Data {
		if forbiddenField(key) {
			return ErrEventSecretField
		}
	}
	if encoded, err := json.Marshal(e.Data); err != nil || len(encoded) > maxDataBytes {
		return ErrEventDataInvalid
	}
	return nil
}

func forbiddenField(key string) bool {
	key = strings.ToLower(strings.ReplaceAll(key, "-", "_"))
	for _, marker := range []string{"secret", "password", "passwd", "token", "credential", "api_key", "private_key"} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	return false
}

// Store is the durable event history boundary. Since returns events strictly
// after the supplied sequence so reconnecting consumers can resume safely.
type Store interface {
	Append(context.Context, Event) (Event, error)
	Since(context.Context, Sequence) ([]Event, error)
}

// Subscription is a read-only live stream. Implementations must not expose a
// send-capable channel to consumers and must provide deterministic cleanup.
type Subscription struct {
	Events <-chan Event
	Close  func()
}

// Bus publishes only after Store.Append has durably committed the event.
type Bus interface {
	Publish(context.Context, Event) error
	Subscribe(context.Context, Sequence) (Subscription, error)
}

// Code is a stable machine-readable event failure reason.
type Code string

const (
	CodeEventTypeInvalid              Code = "EVENT_TYPE_INVALID"
	CodeEventSecretField              Code = "EVENT_SECRET_FIELD"
	CodeEventStoreFailed              Code = "EVENT_STORE_FAILED"
	CodeEventSequenceConflict         Code = "EVENT_SEQUENCE_CONFLICT"
	CodeEventIllegalTransition        Code = "EVENT_ILLEGAL_TRANSITION"
	CodeEventAuthorizationDenied      Code = "EVENT_AUTHORIZATION_DENIED"
	CodeEventAuthorizationStale       Code = "EVENT_AUTHORIZATION_STALE"
	CodeEventAuthorizationUnavailable Code = "EVENT_AUTHORIZATION_UNAVAILABLE"
	CodeEventDataInvalid              Code = "EVENT_DATA_INVALID"
)

var (
	ErrEventTypeInvalid              = &Error{Code: CodeEventTypeInvalid, Message: "event type is invalid"}
	ErrEventSecretField              = &Error{Code: CodeEventSecretField, Message: "event contains a forbidden sensitive field"}
	ErrEventStoreFailed              = &Error{Code: CodeEventStoreFailed, Message: "event store operation failed"}
	ErrEventSequenceConflict         = &Error{Code: CodeEventSequenceConflict, Message: "event sequence conflicts with durable history"}
	ErrEventIllegalTransition        = &Error{Code: CodeEventIllegalTransition, Message: "event lifecycle transition is invalid"}
	ErrEventAuthorizationDenied      = &Error{Code: CodeEventAuthorizationDenied, Message: "event authorization denied"}
	ErrEventAuthorizationStale       = &Error{Code: CodeEventAuthorizationStale, Message: "event authorization is stale"}
	ErrEventAuthorizationUnavailable = &Error{Code: CodeEventAuthorizationUnavailable, Message: "event authorization is unavailable"}
	ErrEventDataInvalid              = &Error{Code: CodeEventDataInvalid, Message: "event data is invalid or exceeds the safety bound"}
)

// Error carries a stable code and a human-safe message. Causes are available
// through errors.Is/As but are never included in the presentation message.
type Error struct {
	Code    Code
	Message string
	Err     error
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.Err }
func (e *Error) Is(target error) bool {
	other, ok := target.(*Error)
	return ok && e.Code == other.Code
}

// NewError creates a safe structured error for an internal cause.
func NewError(code Code, cause error) error {
	return &Error{Code: code, Message: safeMessage(code), Err: cause}
}

// ReasonCode extracts the stable code without parsing a presentation string.
func ReasonCode(err error) Code {
	var eventErr *Error
	if errors.As(err, &eventErr) {
		return eventErr.Code
	}
	return ""
}

func safeMessage(code Code) string {
	switch code {
	case CodeEventTypeInvalid:
		return ErrEventTypeInvalid.Message
	case CodeEventSecretField:
		return ErrEventSecretField.Message
	case CodeEventStoreFailed:
		return ErrEventStoreFailed.Message
	case CodeEventSequenceConflict:
		return ErrEventSequenceConflict.Message
	case CodeEventIllegalTransition:
		return ErrEventIllegalTransition.Message
	case CodeEventAuthorizationDenied:
		return ErrEventAuthorizationDenied.Message
	case CodeEventAuthorizationStale:
		return ErrEventAuthorizationStale.Message
	case CodeEventAuthorizationUnavailable:
		return ErrEventAuthorizationUnavailable.Message
	case CodeEventDataInvalid:
		return ErrEventDataInvalid.Message
	default:
		return "event operation failed"
	}
}
