package capability

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

type GrantID string
type SubjectID string
type TaskID string

type CapabilityKind string

const (
	KindFilesystemRead  CapabilityKind = "fs.read"
	KindFilesystemWrite CapabilityKind = "fs.write"
	KindShellExec       CapabilityKind = "shell.exec"
	KindGitCommit       CapabilityKind = "git.commit"
	KindGitPush         CapabilityKind = "git.push"
	KindNetworkEgress   CapabilityKind = "network.egress"
	KindSecretUse       CapabilityKind = "secret.use"
	KindMCPCall         CapabilityKind = "mcp.call"
	KindDeployExecute   CapabilityKind = "deploy.execute"
)

type GrantState string

const (
	StateRequested GrantState = "requested"
	StateIssued    GrantState = "issued"
	StateActive    GrantState = "active"
	StateRevoked   GrantState = "revoked"
	StateExpired   GrantState = "expired"
)

type DecisionOutcome string

const (
	OutcomeAllow DecisionOutcome = "ALLOW"
	OutcomeDeny  DecisionOutcome = "DENY"
)

type Scope struct {
	Resource    string            `json:"resource"`
	Actions     []string          `json:"actions"`
	Constraints map[string]string `json:"constraints,omitempty"`
}

func (s Scope) Validate() error {
	if strings.TrimSpace(s.Resource) == "" || len(s.Actions) == 0 {
		return ErrInvalidScope
	}
	seen := make(map[string]struct{}, len(s.Actions))
	for _, action := range s.Actions {
		action = strings.TrimSpace(action)
		if action == "" {
			return ErrInvalidScope
		}
		if _, ok := seen[action]; ok {
			return ErrInvalidScope
		}
		seen[action] = struct{}{}
	}
	for key, value := range s.Constraints {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			return ErrInvalidScope
		}
	}
	return nil
}

type Grant struct {
	ID        GrantID        `json:"id"`
	Subject   SubjectID      `json:"subject"`
	TaskID    TaskID         `json:"task_id"`
	Kind      CapabilityKind `json:"kind"`
	Scope     Scope          `json:"scope"`
	IssuedAt  time.Time      `json:"issued_at"`
	ExpiresAt time.Time      `json:"expires_at"`
	Issuer    SubjectID      `json:"issuer"`
	RevokedAt *time.Time     `json:"revoked_at,omitempty"`
}

func (g Grant) Validate() error {
	if strings.TrimSpace(string(g.ID)) == "" {
		return ErrInvalidScope
	}
	return validateGrantFields(g.Subject, g.TaskID, g.Kind, g.Scope, g.IssuedAt, g.ExpiresAt, g.Issuer, g.RevokedAt)
}

func validateGrantFields(subject SubjectID, taskID TaskID, kind CapabilityKind, scope Scope, issuedAt, expiresAt time.Time, issuer SubjectID, revokedAt *time.Time) error {
	if strings.TrimSpace(string(subject)) == "" || strings.TrimSpace(string(taskID)) == "" || strings.TrimSpace(string(issuer)) == "" {
		return ErrInvalidScope
	}
	if !knownKind(kind) || issuedAt.IsZero() || expiresAt.IsZero() || !expiresAt.After(issuedAt) {
		return ErrInvalidScope
	}
	if revokedAt != nil && revokedAt.Before(issuedAt) {
		return ErrInvalidScope
	}
	return scope.Validate()
}

func knownKind(kind CapabilityKind) bool {
	switch kind {
	case KindFilesystemRead, KindFilesystemWrite, KindShellExec, KindGitCommit,
		KindGitPush, KindNetworkEgress, KindSecretUse, KindMCPCall, KindDeployExecute:
		return true
	default:
		return false
	}
}

type GrantRequest struct {
	Subject        SubjectID
	TaskID         TaskID
	Kind           CapabilityKind
	Scope          Scope
	IssuedAt       time.Time
	ExpiresAt      time.Time
	Issuer         SubjectID
	IdempotencyKey string
}

func (r GrantRequest) Validate() error {
	return validateGrantFields(r.Subject, r.TaskID, r.Kind, r.Scope, r.IssuedAt, r.ExpiresAt, r.Issuer, nil)
}

type Query struct {
	Subject  SubjectID
	TaskID   TaskID
	Kind     CapabilityKind
	Resource string
	Action   string
	At       time.Time
}

type Decision struct {
	Outcome      DecisionOutcome `json:"outcome"`
	Reason       ErrorCode       `json:"reason"`
	MatchedGrant GrantID         `json:"matched_grant,omitempty"`
	PolicyDigest string          `json:"policy_digest,omitempty"`
	ExpiresAt    time.Time       `json:"expires_at,omitempty"`
}

type RevokeRequest struct {
	GrantID        GrantID
	Actor          SubjectID
	IdempotencyKey string
}

type Broker interface {
	Grant(context.Context, GrantRequest) (Grant, error)
	Authorize(context.Context, Query) (Decision, error)
	Revoke(context.Context, RevokeRequest) error
}

func normalizeActions(actions []string) []string {
	result := append([]string(nil), actions...)
	sort.Strings(result)
	return result
}

func (s Scope) String() string {
	return fmt.Sprintf("%s:%s", s.Resource, strings.Join(normalizeActions(s.Actions), ","))
}
