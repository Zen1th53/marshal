// Package events defines the provider-neutral structured event contract used
// across MARSHAL. Persistence, sequencing and live delivery are implemented by
// later T43 atomic units; durable history remains authoritative.
package events

import (
	"context"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	MaxIdentifierBytes = 256
	MaxDataEntries     = 256
	MaxDataKeyBytes    = 256
	MaxDataValueBytes  = 4096
)

// Type is one event name from the canonical TERRA event registry.
type Type string

func (t Type) Valid() bool {
	_, ok := canonicalTypes[t]
	return ok
}

type EventID string
type Sequence uint64
type SubjectID string
type TaskID string
type RunID string
type ResourceID string
type EvidenceID string
type IdempotencyKey string

// Event stores only bounded structured data and foreign subsystem references.
// Data is copied at API boundaries; it must never contain raw secret fields.
type Event struct {
	ID             EventID
	Sequence       Sequence
	Type           Type
	Subject        SubjectID
	TaskID         TaskID
	RunID          RunID
	ResourceID     ResourceID
	EvidenceID     EvidenceID
	At             time.Time
	Data           map[string]string
	IdempotencyKey IdempotencyKey
}

func (e Event) Validate() error {
	if !validRequired(string(e.ID)) || !e.Type.Valid() || !validRequired(string(e.Subject)) || !validRequired(string(e.IdempotencyKey)) {
		if !e.Type.Valid() {
			return ErrInvalidType
		}
		return ErrInvalidEvent
	}
	for _, value := range []string{string(e.TaskID), string(e.RunID), string(e.ResourceID), string(e.EvidenceID)} {
		if value != "" && !validOptional(value) {
			return ErrInvalidEvent
		}
	}
	if !e.At.IsZero() && e.At.Location() != time.UTC {
		return ErrInvalidEvent
	}
	if len(e.Data) > MaxDataEntries {
		return ErrInvalidEvent
	}
	for key, value := range e.Data {
		if key == "" || len(key) > MaxDataKeyBytes || len(value) > MaxDataValueBytes || !validText(key) || !validText(value) {
			return ErrInvalidEvent
		}
		if sensitiveKey(key) {
			return ErrSecretField
		}
	}
	return nil
}

// CloneEvent prevents caller mutation from rewriting an already validated
// event value after it crosses the service/store boundary.
func CloneEvent(event Event) Event {
	clone := event
	if event.Data != nil {
		clone.Data = make(map[string]string, len(event.Data))
		for key, value := range event.Data {
			clone.Data[key] = value
		}
	}
	return clone
}

// Store is the durable event history boundary. Append returns the canonical
// stored event, including sequence/time assigned by persistence.
type Store interface {
	Append(context.Context, Event) (Event, error)
	Since(context.Context, Sequence, int) ([]Event, error)
}

// Bus is the non-authoritative live delivery boundary. Subscribers resume from
// durable Store.Since when they miss events.
type Bus interface {
	Publish(context.Context, Event) error
	Subscribe(context.Context, Sequence) (<-chan Event, func(), error)
}

func validRequired(value string) bool {
	return value != "" && len(value) <= MaxIdentifierBytes && validText(value)
}

func validOptional(value string) bool {
	return len(value) <= MaxIdentifierBytes && validText(value)
}

func validText(value string) bool {
	if !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func sensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	switch normalized {
	case "authorization", "password", "private_key", "secret", "token":
		return true
	default:
		return false
	}
}
