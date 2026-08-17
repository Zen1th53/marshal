package capability

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Kind is a closed vocabulary for privileged capability classes.
type Kind string

const (
	KindFilesystemRead  Kind = "fs.read"
	KindFilesystemWrite Kind = "fs.write"
	KindShellExec       Kind = "shell.exec"
	KindGitCommit       Kind = "git.commit"
	KindGitPush         Kind = "git.push"
	KindNetworkEgress   Kind = "network.egress"
	KindSecretUse       Kind = "secret.use"
	KindMCPCall         Kind = "mcp.call"
	KindDeployExecute   Kind = "deploy.execute"
)

func (r Reason) Valid() bool {
	switch r {
	case ReasonAllowed, ReasonDenied, ReasonExpired, ReasonRevoked, ReasonSubjectMismatch, ReasonTaskMismatch, ReasonInvalidScope:
		return true
	default:
		return false
	}
}

func (k Kind) Valid() bool {
	switch k {
	case KindFilesystemRead, KindFilesystemWrite, KindShellExec, KindGitCommit,
		KindGitPush, KindNetworkEgress, KindSecretUse, KindMCPCall, KindDeployExecute:
		return true
	default:
		return false
	}
}

// Scope is the normalized resource and action boundary for a grant.
type Scope struct {
	Resource    string            `json:"resource"`
	Actions     []string          `json:"actions"`
	Constraints map[string]string `json:"constraints,omitempty"`
}

func (s Scope) Validate() error {
	if strings.TrimSpace(s.Resource) == "" || len(s.Actions) == 0 {
		return ErrInvalidScope
	}
	for _, action := range s.Actions {
		if strings.TrimSpace(action) == "" {
			return ErrInvalidScope
		}
	}
	return nil
}

type GrantState string

const (
	GrantRequested GrantState = "requested"
	GrantIssued    GrantState = "issued"
	GrantActive    GrantState = "active"
	GrantRevoked   GrantState = "revoked"
	GrantExpired   GrantState = "expired"
)

func (s GrantState) Valid() bool {
	switch s {
	case GrantRequested, GrantIssued, GrantActive, GrantRevoked, GrantExpired:
		return true
	default:
		return false
	}
}

type Grant struct {
	ID           string     `json:"id"`
	Subject      string     `json:"subject"`
	TaskID       string     `json:"task_id"`
	Kind         Kind       `json:"kind"`
	Scope        Scope      `json:"scope"`
	IssuedAt     time.Time  `json:"issued_at"`
	ExpiresAt    time.Time  `json:"expires_at"`
	Issuer       string     `json:"issuer"`
	State        GrantState `json:"state"`
	PolicyDigest string     `json:"policy_digest,omitempty"`
	RevokedAt    *time.Time `json:"revoked_at,omitempty"`
}

func (g Grant) Validate() error {
	if !nonEmpty(g.ID) || !nonEmpty(g.Subject) || !nonEmpty(g.TaskID) || !nonEmpty(g.Issuer) || !g.Kind.Valid() || !g.State.Valid() {
		return ErrInvalidGrant
	}
	if err := g.Scope.Validate(); err != nil {
		return ErrInvalidGrant
	}
	if g.IssuedAt.IsZero() || g.ExpiresAt.IsZero() || !g.ExpiresAt.After(g.IssuedAt) {
		return ErrInvalidGrant
	}
	if g.RevokedAt != nil && g.RevokedAt.Before(g.IssuedAt) {
		return ErrInvalidGrant
	}
	return nil
}

type GrantRequest struct {
	Subject string
	TaskID  string
	Kind    Kind
	Scope   Scope
	TTL     time.Duration
	Issuer  string
}

type GrantRepository interface {
	SaveGrant(context.Context, Grant) error
	LoadGrant(context.Context, string) (Grant, error)
	ListGrants(context.Context, Kind) ([]Grant, error)
	RevokeGrant(context.Context, string, time.Time) error
}

type Authority interface {
	AuthorizeGrant(context.Context, GrantRequest) error
	AuthorizeRevoke(context.Context, RevokeRequest) error
}

type AuditEvent struct {
	ID        string
	Type      string
	GrantID   string
	Subject   string
	TaskID    string
	Kind      Kind
	Resource  string
	Reason    Reason
	Timestamp time.Time
}

type AuditSink interface {
	AppendCapabilityEvent(context.Context, AuditEvent) error
}

type RevokeRequest struct {
	ID    string
	Actor string
}

type Query struct {
	Subject  string
	TaskID   string
	Kind     Kind
	Resource string
	Action   string
}

type Reason string

const (
	ReasonAllowed         Reason = "CAP_ALLOWED"
	ReasonDenied          Reason = "CAP_DENIED"
	ReasonExpired         Reason = "CAP_EXPIRED"
	ReasonRevoked         Reason = "CAP_REVOKED"
	ReasonSubjectMismatch Reason = "CAP_SUBJECT_MISMATCH"
	ReasonTaskMismatch    Reason = "CAP_TASK_MISMATCH"
	ReasonInvalidScope    Reason = "CAP_INVALID_SCOPE"
)

type Decision struct {
	Allowed bool   `json:"allowed"`
	Reason  Reason `json:"reason"`
	GrantID string `json:"grant_id,omitempty"`
}

func (d Decision) Validate() error {
	switch d.Reason {
	case ReasonAllowed:
		if !d.Allowed || !nonEmpty(d.GrantID) {
			return ErrInvalidDecision
		}
	case ReasonDenied, ReasonExpired, ReasonRevoked, ReasonSubjectMismatch,
		ReasonTaskMismatch, ReasonInvalidScope:
		if d.Allowed {
			return ErrInvalidDecision
		}
	default:
		return ErrInvalidDecision
	}
	return nil
}

type Broker interface {
	Grant(context.Context, GrantRequest) (Grant, error)
	Authorize(context.Context, Query) (Decision, error)
	Revoke(context.Context, RevokeRequest) error
}

var (
	ErrCapability      = errors.New("capability error")
	ErrInvalidScope    = errors.New("invalid capability scope")
	ErrInvalidGrant    = errors.New("invalid capability grant")
	ErrInvalidDecision = errors.New("invalid capability decision")
	ErrDenied          = errors.Join(ErrCapability, errors.New("capability denied"))
	ErrGrantNotFound   = errors.Join(ErrCapability, errors.New("capability grant not found"))
)

func nonEmpty(value string) bool { return strings.TrimSpace(value) != "" }
