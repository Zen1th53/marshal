package secrets

import (
	"context"
	"strings"
	"time"
)

// Ref identifies a secret without containing its value.
type Ref struct {
	Provider string `json:"provider"`
	Name     string `json:"name"`
	Version  string `json:"version"`
}

func (r Ref) Validate() error {
	if invalidIdentifier(r.Provider) || invalidIdentifier(r.Name) || invalidIdentifier(r.Version) {
		return ErrNotFound
	}
	return nil
}

// Lease is the bounded authority to use one secret for one task and purpose.
// It deliberately contains only the reference, never the resolved value.
type Lease struct {
	ID        string    `json:"id"`
	Subject   string    `json:"subject"`
	TaskID    string    `json:"task_id"`
	Ref       Ref       `json:"ref"`
	Purpose   string    `json:"purpose"`
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (l Lease) Validate() error {
	if invalidIdentifier(l.ID) || invalidIdentifier(l.Subject) || invalidIdentifier(l.TaskID) || invalidIdentifier(l.Purpose) || l.IssuedAt.IsZero() || l.ExpiresAt.IsZero() || !l.ExpiresAt.After(l.IssuedAt) {
		return ErrDenied
	}
	return l.Ref.Validate()
}

type LeaseRequest struct {
	Subject   string
	TaskID    string
	Ref       Ref
	Purpose   string
	IssuedAt  time.Time
	ExpiresAt time.Time
}

type RevokeRequest struct {
	LeaseID string
	Subject string
}

// Provider resolves a reference only at the narrow secret-use boundary.
type Provider interface {
	Resolve(context.Context, Ref) ([]byte, error)
}

// Broker is the sole public secret-use boundary. Implementations must not
// expose resolved bytes outside WithSecret's callback.
type Broker interface {
	Lease(context.Context, LeaseRequest) (Lease, error)
	WithSecret(context.Context, Lease, func([]byte) error) error
	Revoke(context.Context, RevokeRequest) error
}

func invalidIdentifier(value string) bool {
	value = strings.TrimSpace(value)
	return value == "" || strings.IndexByte(value, '\x00') >= 0
}
