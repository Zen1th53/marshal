// Package protocol defines the provider-neutral typed handoff boundary.
package protocol

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"
)

type HandoffID string
type TaskID string
type EvidenceID string
type Role string
type Status string
type Action string
type Reason string
type ErrorCode string

const (
	Version1 = 1

	RoleOrchestrator Role = "orchestrator"
	RoleArchitect    Role = "architect"
	RoleDeveloper    Role = "developer"
	RoleQA           Role = "qa"
	RoleAppSec       Role = "appsec"

	StatusCreated   Status = "created"
	StatusValidated Status = "validated"
	StatusAccepted  Status = "accepted"
	StatusRejected  Status = "rejected"
	StatusConsumed  Status = "consumed"

	ActionCreate  Action = "create"
	ActionConsume Action = "consume"

	ReasonAccepted           Reason = "HANDOFF_ACCEPTED"
	ReasonVersionUnsupported Reason = "HANDOFF_VERSION_UNSUPPORTED"
	ReasonSenderForged       Reason = "HANDOFF_SENDER_FORGED"
	ReasonForeignTask        Reason = "HANDOFF_FOREIGN_TASK"
	ReasonEvidenceInvalid    Reason = "HANDOFF_EVIDENCE_INVALID"
	ReasonTooLarge           Reason = "HANDOFF_TOO_LARGE"

	CodeVersionUnsupported ErrorCode = "HANDOFF_VERSION_UNSUPPORTED"
	CodeSenderForged       ErrorCode = "HANDOFF_SENDER_FORGED"
	CodeForeignTask        ErrorCode = "HANDOFF_FOREIGN_TASK"
	CodeEvidenceInvalid    ErrorCode = "HANDOFF_EVIDENCE_INVALID"
	CodeTooLarge           ErrorCode = "HANDOFF_TOO_LARGE"
	CodeAuthorityTransfer  ErrorCode = "HANDOFF_AUTHORITY_TRANSFER"
	CodeTransitionInvalid  ErrorCode = "HANDOFF_TRANSITION_INVALID"
	CodeAuthorization      ErrorCode = "HANDOFF_AUTHORIZATION_DENIED"
	CodeUnavailable        ErrorCode = "HANDOFF_UNAVAILABLE"
	CodeInvalid            ErrorCode = "HANDOFF_INVALID"
	CodeNotFound           ErrorCode = "HANDOFF_NOT_FOUND"
)

const (
	maxClaims        = 32
	maxEvidenceIDs   = 64
	maxChangedFiles  = 256
	maxRisks         = 64
	maxUnresolved    = 64
	maxTextBytes     = 4096
	maxIdentifierLen = 256
)

type Error struct{ Code ErrorCode }

func (e *Error) Error() string { return string(e.Code) }
func (e *Error) Is(target error) bool {
	other, ok := target.(*Error)
	return ok && e != nil && other != nil && e.Code == other.Code
}

var (
	ErrVersionUnsupported = &Error{Code: CodeVersionUnsupported}
	ErrSenderForged       = &Error{Code: CodeSenderForged}
	ErrForeignTask        = &Error{Code: CodeForeignTask}
	ErrEvidenceInvalid    = &Error{Code: CodeEvidenceInvalid}
	ErrTooLarge           = &Error{Code: CodeTooLarge}
	ErrAuthorityTransfer  = &Error{Code: CodeAuthorityTransfer}
	ErrTransitionInvalid  = &Error{Code: CodeTransitionInvalid}
	ErrAuthorization      = &Error{Code: CodeAuthorization}
	ErrUnavailable        = &Error{Code: CodeUnavailable}
	ErrInvalid            = &Error{Code: CodeInvalid}
	ErrHandoffNotFound    = &Error{Code: CodeNotFound}
)

type Principal struct {
	ID   string `json:"id"`
	Role Role   `json:"role"`
}

// Handoff is bounded data and evidence references. It deliberately has no
// capability, authority, token, or raw-secret field.
type Handoff struct {
	ID             HandoffID         `json:"id"`
	Version        int               `json:"version"`
	TaskID         TaskID            `json:"task_id"`
	FromAgent      string            `json:"from_agent"`
	ToRole         Role              `json:"to_role"`
	Status         Status            `json:"status"`
	Claims         map[string]string `json:"claims"`
	EvidenceIDs    []EvidenceID      `json:"evidence_ids"`
	ChangedFiles   []string          `json:"changed_files"`
	Risks          []string          `json:"risks,omitempty"`
	Unresolved     []string          `json:"unresolved,omitempty"`
	ContextDigest  string            `json:"context_digest"`
	CreatedAt      time.Time         `json:"created_at"`
	ConsumedAt     *time.Time        `json:"consumed_at,omitempty"`
	IdempotencyKey string            `json:"idempotency_key"`
}

type Submission struct {
	IdempotencyKey string  `json:"idempotency_key"`
	Handoff        Handoff `json:"handoff"`
}

type AuthorizationDecision struct {
	Allowed    bool      `json:"allowed"`
	Reason     Reason    `json:"reason"`
	FreshUntil time.Time `json:"fresh_until"`
}

type Authorizer interface {
	Authorize(context.Context, Action, Principal, Handoff) (AuthorizationDecision, error)
}

// Repository supplies the typed canonical record. The legacy handoffs table
// is intentionally outside this API and remains compatibility history only.
type Repository interface {
	Create(context.Context, Handoff) (Handoff, error)
	Transition(context.Context, HandoffID, Status, Status, Principal) (Handoff, error)
	Get(context.Context, HandoffID) (Handoff, error)
	EvidenceBelongsToTask(context.Context, TaskID, []EvidenceID) error
}

func (h Handoff) Validate() error {
	if h.Version != Version1 {
		return ErrVersionUnsupported
	}
	if !validID(string(h.ID)) || !validID(string(h.TaskID)) || !validID(h.FromAgent) || !validID(h.IdempotencyKey) ||
		!validRole(h.ToRole) || !validStatus(h.Status) || !validDigest(h.ContextDigest) || h.CreatedAt.IsZero() || h.CreatedAt.Location() != time.UTC {
		return ErrInvalid
	}
	if len(h.Claims) > maxClaims || len(h.EvidenceIDs) > maxEvidenceIDs || len(h.ChangedFiles) > maxChangedFiles || len(h.Risks) > maxRisks || len(h.Unresolved) > maxUnresolved {
		return ErrTooLarge
	}
	for key, value := range h.Claims {
		if !validText(key) || !validText(value) || sensitiveOrAuthorityField(key) {
			return ErrAuthorityTransfer
		}
	}
	for _, evidenceID := range h.EvidenceIDs {
		if !validID(string(evidenceID)) {
			return ErrEvidenceInvalid
		}
	}
	for _, values := range [][]string{h.ChangedFiles, h.Risks, h.Unresolved} {
		for _, value := range values {
			if !validText(value) {
				return ErrInvalid
			}
		}
	}
	return nil
}

func cloneHandoff(h Handoff) Handoff {
	copy := h
	copy.Claims = make(map[string]string, len(h.Claims))
	for key, value := range h.Claims {
		copy.Claims[key] = value
	}
	copy.EvidenceIDs = append([]EvidenceID(nil), h.EvidenceIDs...)
	copy.ChangedFiles = append([]string(nil), h.ChangedFiles...)
	copy.Risks = append([]string(nil), h.Risks...)
	copy.Unresolved = append([]string(nil), h.Unresolved...)
	if h.ConsumedAt != nil {
		value := *h.ConsumedAt
		copy.ConsumedAt = &value
	}
	return copy
}

func validRole(role Role) bool {
	switch role {
	case RoleOrchestrator, RoleArchitect, RoleDeveloper, RoleQA, RoleAppSec:
		return true
	default:
		return false
	}
}

func validStatus(status Status) bool {
	switch status {
	case StatusCreated, StatusValidated, StatusAccepted, StatusRejected, StatusConsumed:
		return true
	default:
		return false
	}
}

func validID(value string) bool {
	return len(value) > 0 && len(value) <= maxIdentifierLen && validText(value) && !strings.ContainsAny(value, "/\\")
}

func validText(value string) bool {
	return len(value) > 0 && len(value) <= maxTextBytes && utf8.ValidString(value) && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\r\n\t")
}

func validDigest(value string) bool {
	if len(value) != 71 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if !(character >= 'a' && character <= 'f') && !(character >= '0' && character <= '9') {
			return false
		}
	}
	return true
}

func sensitiveOrAuthorityField(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	return strings.Contains(key, "secret") || strings.Contains(key, "token") || strings.Contains(key, "password") ||
		strings.Contains(key, "private_key") || strings.Contains(key, "capabil") || strings.Contains(key, "authorit")
}
