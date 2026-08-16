package capability

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Zen1th53/marshal/internal/events"
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

type Authority interface {
	AuthorizeGrant(context.Context, GrantRequest) error
	AuthorizeRevoke(context.Context, RevokeRequest, Grant) error
}

type Engine struct {
	repository GrantRepository
	now        func() time.Time
	authority  Authority
	eventStore events.Store
}

var _ Broker = (*Engine)(nil)

func NewEngine(repository GrantRepository, now func() time.Time) *Engine {
	return NewAuthorizedEngine(repository, now, nil)
}

func NewAuthorizedEngine(repository GrantRepository, now func() time.Time, authority Authority) *Engine {
	if now == nil {
		now = time.Now
	}
	return &Engine{repository: repository, now: now, authority: authority}
}

func NewAuditedEngine(repository GrantRepository, now func() time.Time, authority Authority, eventStore events.Store) *Engine {
	engine := NewAuthorizedEngine(repository, now, authority)
	engine.eventStore = eventStore
	return engine
}

func (e *Engine) Grant(ctx context.Context, request GrantRequest) (Grant, error) {
	if e == nil || e.repository == nil || e.authority == nil || strings.TrimSpace(request.IdempotencyKey) == "" {
		return Grant{}, ErrDenied
	}
	now := e.now().UTC()
	validation := request
	if validation.IssuedAt.IsZero() {
		validation.IssuedAt = now
	}
	if err := validation.Validate(); err != nil {
		return Grant{}, err
	}
	resource, err := NormalizeResource(request.Kind, request.Scope.Resource)
	if err != nil {
		return Grant{}, err
	}
	request.Scope.Resource = resource
	if err := e.authority.AuthorizeGrant(ctx, request); err != nil {
		return Grant{}, ErrDenied
	}
	if !request.ExpiresAt.After(now) {
		return Grant{}, ErrExpired
	}
	if existing, found, err := e.repository.FindCapabilityGrantByIdempotencyKey(ctx, request.IdempotencyKey); err != nil {
		return Grant{}, err
	} else if found {
		if grantRequestMatches(existing, request) {
			if err := e.emitGrantEvent(ctx, events.Type("capability.grant.requested"), existing); err != nil {
				return Grant{}, err
			}
			if err := e.emitGrantEvent(ctx, events.Type("capability.grant.issued"), existing); err != nil {
				return Grant{}, err
			}
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
	if err := e.emitGrantEvent(ctx, events.Type("capability.grant.requested"), grant); err != nil {
		return Grant{}, err
	}
	if err := e.emitGrantEvent(ctx, events.Type("capability.grant.issued"), grant); err != nil {
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
	resource, err := NormalizeResource(query.Kind, query.Resource)
	if err != nil {
		return Decision{}, err
	}
	query.Resource = resource
	grants, err := e.repository.ListCapabilityGrants(ctx)
	if err != nil {
		return Decision{}, err
	}
	sort.Slice(grants, func(i, j int) bool { return grants[i].ID < grants[j].ID })
	var subjectMismatch, taskMismatch bool
	for _, grant := range grants {
		grantResource, normalizeErr := NormalizeResource(grant.Kind, grant.Scope.Resource)
		if normalizeErr != nil || grant.Kind != query.Kind || grantResource != query.Resource || !scopeAllowsAction(grant.Scope, query.Action) {
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
			return e.recordDecision(ctx, query, Decision{Outcome: OutcomeDeny, Reason: CodeRevoked, MatchedGrant: grant.ID, ExpiresAt: grant.ExpiresAt})
		}
		if !at.Before(grant.ExpiresAt) {
			return e.recordDecision(ctx, query, Decision{Outcome: OutcomeDeny, Reason: CodeExpired, MatchedGrant: grant.ID, ExpiresAt: grant.ExpiresAt})
		}
		if at.Before(grant.IssuedAt) {
			return e.recordDecision(ctx, query, Decision{Outcome: OutcomeDeny, Reason: CodeDenied, MatchedGrant: grant.ID, ExpiresAt: grant.ExpiresAt})
		}
		return e.recordDecision(ctx, query, Decision{Outcome: OutcomeAllow, MatchedGrant: grant.ID, ExpiresAt: grant.ExpiresAt, PolicyDigest: grant.PolicyDigest})
	}
	if subjectMismatch {
		return e.recordDecision(ctx, query, Decision{Outcome: OutcomeDeny, Reason: CodeSubjectMismatch})
	}
	if taskMismatch {
		return e.recordDecision(ctx, query, Decision{Outcome: OutcomeDeny, Reason: CodeTaskMismatch})
	}
	return e.recordDecision(ctx, query, Decision{Outcome: OutcomeDeny, Reason: CodeDenied})
}

func (e *Engine) Revoke(ctx context.Context, request RevokeRequest) error {
	if e == nil || e.repository == nil || e.authority == nil || strings.TrimSpace(string(request.GrantID)) == "" || strings.TrimSpace(string(request.Actor)) == "" {
		return ErrDenied
	}
	grant, err := e.repository.GetCapabilityGrant(ctx, request.GrantID)
	if err != nil {
		return err
	}
	if grant.RevokedAt != nil {
		return e.emitGrantEvent(ctx, events.Type("capability.grant.revoked"), grant)
	}
	if grant.Issuer != request.Actor {
		return ErrSubjectMismatch
	}
	if err := e.authority.AuthorizeRevoke(ctx, request, grant); err != nil {
		return ErrDenied
	}
	if err := e.repository.RevokeCapabilityGrant(ctx, request.GrantID, e.now().UTC()); err != nil {
		return err
	}
	return e.emitGrantEvent(ctx, events.Type("capability.grant.revoked"), grant)
}

func (e *Engine) emitGrantEvent(ctx context.Context, eventType events.Type, grant Grant) error {
	if e.eventStore == nil {
		return nil
	}
	resourceID := resourceReference(grant.Scope.Resource)
	key := eventKey(string(eventType), string(grant.ID))
	_, err := e.eventStore.Append(ctx, events.Event{
		ID: events.EventID(key), Type: eventType, Subject: events.SubjectID(grant.Subject),
		TaskID: events.TaskID(grant.TaskID), ResourceID: events.ResourceID(resourceID),
		At: e.now().UTC(), IdempotencyKey: events.IdempotencyKey(key),
		Data: map[string]string{"grant_id": string(grant.ID), "kind": string(grant.Kind)},
	})
	if err != nil {
		return ErrDenied
	}
	return nil
}

func (e *Engine) recordDecision(ctx context.Context, query Query, decision Decision) (Decision, error) {
	if e.eventStore == nil {
		return decision, nil
	}
	eventType := events.Type("capability.authorize.denied")
	if decision.Outcome == OutcomeAllow {
		eventType = events.Type("capability.authorize.allowed")
	}
	key := eventKey(string(eventType), string(query.Subject), string(query.TaskID), string(query.Kind), query.Resource, query.Action)
	data := map[string]string{"kind": string(query.Kind), "reason": string(decision.Reason)}
	if decision.MatchedGrant != "" {
		data["grant_id"] = string(decision.MatchedGrant)
	}
	_, err := e.eventStore.Append(ctx, events.Event{
		ID: events.EventID(key), Type: eventType, Subject: events.SubjectID(query.Subject),
		TaskID: events.TaskID(query.TaskID), ResourceID: events.ResourceID(resourceReference(query.Resource)),
		At: e.now().UTC(), IdempotencyKey: events.IdempotencyKey(key), Data: data,
	})
	if err != nil {
		return decision, ErrDenied
	}
	return decision, nil
}

func eventKey(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "cap-event-" + hex.EncodeToString(sum[:])
}

func resourceReference(resource string) string {
	sum := sha256.Sum256([]byte(resource))
	return "cap-resource-" + hex.EncodeToString(sum[:])
}

// NormalizeResource canonicalizes resource identity without touching the
// filesystem or resolving symlinks.
func NormalizeResource(kind CapabilityKind, resource string) (string, error) {
	if !utf8.ValidString(resource) || strings.TrimSpace(resource) == "" {
		return "", ErrInvalidScope
	}
	for _, r := range resource {
		if unicode.IsControl(r) {
			return "", ErrInvalidScope
		}
	}
	resource = strings.TrimSpace(resource)
	if kind == KindFilesystemRead || kind == KindFilesystemWrite {
		clean := filepath.Clean(resource)
		if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return "", ErrInvalidScope
		}
		return clean, nil
	}
	return resource, nil
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
