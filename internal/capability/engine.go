package capability

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// GrantRepository is the narrow durable boundary used by the broker. The
// broker owns matching and state semantics; the repository owns transactions.
type GrantRepository interface {
	PutCapabilityGrant(context.Context, Grant) error
	GetCapabilityGrant(context.Context, GrantID) (Grant, error)
	FindCapabilityGrantByIdempotencyKey(context.Context, string) (Grant, bool, error)
	ListCapabilityGrants(context.Context) ([]Grant, error)
	RevokeCapabilityGrant(context.Context, GrantID, time.Time) error
}

type Engine struct {
	repository GrantRepository
	now        func() time.Time
}

var _ Broker = (*Engine)(nil)

func NewEngine(repository GrantRepository, now func() time.Time) *Engine {
	if now == nil {
		now = time.Now
	}
	return &Engine{repository: repository, now: now}
}

func (e *Engine) Grant(ctx context.Context, request GrantRequest) (Grant, error) {
	if e == nil || e.repository == nil || strings.TrimSpace(request.IdempotencyKey) == "" {
		return Grant{}, ErrInvalidScope
	}
	now := e.now().UTC()
	validation := request
	if validation.IssuedAt.IsZero() {
		validation.IssuedAt = now
	}
	if err := validation.Validate(); err != nil {
		return Grant{}, err
	}
	if !request.ExpiresAt.After(now) {
		return Grant{}, ErrExpired
	}
	if existing, found, err := e.repository.FindCapabilityGrantByIdempotencyKey(ctx, request.IdempotencyKey); err != nil {
		return Grant{}, err
	} else if found {
		if grantRequestMatches(existing, request) {
			return existing, nil
		}
		return Grant{}, ErrDenied
	}
	grant := Grant{
		ID:             deterministicGrantID(request),
		Subject:        request.Subject,
		TaskID:         request.TaskID,
		Kind:           request.Kind,
		Scope:          request.Scope,
		IssuedAt:       now,
		ExpiresAt:      request.ExpiresAt.UTC(),
		Issuer:         request.Issuer,
		IdempotencyKey: request.IdempotencyKey,
	}
	if err := e.repository.PutCapabilityGrant(ctx, grant); err != nil {
		return Grant{}, err
	}
	return grant, nil
}

func (e *Engine) Authorize(ctx context.Context, query Query) (Decision, error) {
	if e == nil || e.repository == nil || strings.TrimSpace(string(query.Subject)) == "" ||
		strings.TrimSpace(string(query.TaskID)) == "" || !knownKind(query.Kind) ||
		strings.TrimSpace(query.Resource) == "" || strings.TrimSpace(query.Action) == "" {
		return Decision{}, ErrInvalidScope
	}
	at := query.At.UTC()
	if query.At.IsZero() {
		at = e.now().UTC()
	}
	grants, err := e.repository.ListCapabilityGrants(ctx)
	if err != nil {
		return Decision{}, err
	}
	sort.Slice(grants, func(i, j int) bool { return grants[i].ID < grants[j].ID })
	var subjectMismatch, taskMismatch bool
	for _, grant := range grants {
		if grant.Kind != query.Kind || grant.Scope.Resource != query.Resource || !scopeAllowsAction(grant.Scope, query.Action) {
			continue
		}
		if grant.Subject != query.Subject {
			subjectMismatch = true
			continue
		}
		if grant.TaskID != query.TaskID {
			taskMismatch = true
			continue
		}
		if grant.RevokedAt != nil {
			return Decision{Outcome: OutcomeDeny, Reason: CodeRevoked, MatchedGrant: grant.ID, ExpiresAt: grant.ExpiresAt}, nil
		}
		if !at.Before(grant.ExpiresAt) {
			return Decision{Outcome: OutcomeDeny, Reason: CodeExpired, MatchedGrant: grant.ID, ExpiresAt: grant.ExpiresAt}, nil
		}
		if at.Before(grant.IssuedAt) {
			return Decision{Outcome: OutcomeDeny, Reason: CodeDenied, MatchedGrant: grant.ID, ExpiresAt: grant.ExpiresAt}, nil
		}
		return Decision{Outcome: OutcomeAllow, Reason: "", MatchedGrant: grant.ID, ExpiresAt: grant.ExpiresAt, PolicyDigest: grant.PolicyDigest}, nil
	}
	if subjectMismatch {
		return Decision{Outcome: OutcomeDeny, Reason: CodeSubjectMismatch}, nil
	}
	if taskMismatch {
		return Decision{Outcome: OutcomeDeny, Reason: CodeTaskMismatch}, nil
	}
	return Decision{Outcome: OutcomeDeny, Reason: CodeDenied}, nil
}

func (e *Engine) Revoke(ctx context.Context, request RevokeRequest) error {
	if e == nil || e.repository == nil || strings.TrimSpace(string(request.GrantID)) == "" || strings.TrimSpace(string(request.Actor)) == "" {
		return ErrInvalidScope
	}
	grant, err := e.repository.GetCapabilityGrant(ctx, request.GrantID)
	if err != nil {
		return err
	}
	if grant.Issuer != request.Actor {
		return ErrSubjectMismatch
	}
	return e.repository.RevokeCapabilityGrant(ctx, request.GrantID, e.now().UTC())
}

func deterministicGrantID(request GrantRequest) GrantID {
	canonical := struct {
		Subject, TaskID, Kind, Resource, IdempotencyKey string
		Actions                                         []string
		Constraints                                     map[string]string
		ExpiresAt, Issuer                               string
	}{
		Subject: string(request.Subject), TaskID: string(request.TaskID), Kind: string(request.Kind),
		Resource: request.Scope.Resource, Actions: append([]string(nil), request.Scope.Actions...),
		Constraints: request.Scope.Constraints, IdempotencyKey: request.IdempotencyKey,
		ExpiresAt: request.ExpiresAt.UTC().Format(time.RFC3339Nano), Issuer: string(request.Issuer),
	}
	sort.Strings(canonical.Actions)
	data, _ := json.Marshal(canonical)
	sum := sha256.Sum256(data)
	return GrantID("cap-" + hex.EncodeToString(sum[:]))
}

func grantRequestMatches(grant Grant, request GrantRequest) bool {
	return grant.Subject == request.Subject && grant.TaskID == request.TaskID && grant.Kind == request.Kind &&
		grant.Scope.Resource == request.Scope.Resource && scopeActionsEqual(grant.Scope, request.Scope) &&
		scopeConstraintsEqual(grant.Scope, request.Scope) && grant.ExpiresAt.Equal(request.ExpiresAt.UTC()) &&
		grant.Issuer == request.Issuer
}

func scopeAllowsAction(scope Scope, action string) bool {
	for _, candidate := range scope.Actions {
		if strings.TrimSpace(candidate) == action {
			return true
		}
	}
	return false
}

func scopeActionsEqual(left, right Scope) bool {
	a, b := append([]string(nil), left.Actions...), append([]string(nil), right.Actions...)
	sort.Strings(a)
	sort.Strings(b)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if strings.TrimSpace(a[i]) != strings.TrimSpace(b[i]) {
			return false
		}
	}
	return true
}

func scopeConstraintsEqual(left, right Scope) bool {
	if len(left.Constraints) != len(right.Constraints) {
		return false
	}
	for key, value := range left.Constraints {
		if right.Constraints[key] != value {
			return false
		}
	}
	return true
}
