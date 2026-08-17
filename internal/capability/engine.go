package capability

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// Engine is the single capability decision path. Persistence is injected so
// this package cannot accidentally create a second source of truth.
type Engine struct {
	repository GrantRepository
	now        func() time.Time
	authority  Authority
	audit      AuditSink
}

func NewEngine(repository GrantRepository, now func() time.Time, authority Authority) *Engine {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Engine{repository: repository, now: now, authority: authority}
}

func NewEngineWithAudit(repository GrantRepository, now func() time.Time, authority Authority, audit AuditSink) *Engine {
	engine := NewEngine(repository, now, authority)
	engine.audit = audit
	return engine
}

func (e *Engine) Grant(ctx context.Context, request GrantRequest) (Grant, error) {
	if err := ctx.Err(); err != nil {
		return Grant{}, err
	}
	if !nonEmpty(request.Subject) || !nonEmpty(request.TaskID) || !nonEmpty(request.Issuer) || !request.Kind.Valid() || request.TTL <= 0 {
		return Grant{}, ErrInvalidGrant
	}
	if err := request.Scope.Validate(); err != nil {
		return Grant{}, err
	}
	if e.authority == nil {
		return Grant{}, ErrDenied
	}
	if err := e.authority.AuthorizeGrant(ctx, request); err != nil {
		return Grant{}, err
	}
	id, err := model.NewID("cap-")
	if err != nil {
		return Grant{}, fmt.Errorf("create capability grant: %w", err)
	}
	now := e.now().UTC()
	grant := Grant{ID: id, Subject: request.Subject, TaskID: request.TaskID, Kind: request.Kind,
		Scope: request.Scope, IssuedAt: now, ExpiresAt: now.Add(request.TTL), Issuer: request.Issuer, State: GrantActive}
	if err := grant.Validate(); err != nil {
		return Grant{}, err
	}
	if e.repository == nil {
		return Grant{}, ErrCapability
	}
	if err := e.repository.SaveGrant(ctx, grant); err != nil {
		return Grant{}, err
	}
	if err := e.appendAudit(ctx, AuditEvent{ID: "capability.grant.issued:" + grant.ID, Type: "capability.grant.issued", GrantID: grant.ID, Subject: grant.Subject, TaskID: grant.TaskID, Kind: grant.Kind, Resource: canonicalResource(grant.Scope.Resource), Reason: ReasonAllowed, Timestamp: now}); err != nil {
		return Grant{}, err
	}
	return grant, nil
}

func (e *Engine) Authorize(ctx context.Context, query Query) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}
	if !nonEmpty(query.Subject) || !nonEmpty(query.TaskID) || !query.Kind.Valid() || !nonEmpty(query.Resource) || !nonEmpty(query.Action) {
		decision := Decision{Reason: ReasonInvalidScope}
		return decision, e.appendAudit(ctx, e.decisionEvent(query, decision))
	}
	if e.repository == nil {
		return Decision{}, ErrCapability
	}
	grants, err := e.repository.ListGrants(ctx, query.Kind)
	if err != nil {
		return Decision{}, err
	}
	resource := canonicalResource(query.Resource)
	hadSubject := false
	hadTask := false
	now := e.now().UTC()
	for _, grant := range grants {
		if grant.Subject != query.Subject {
			continue
		}
		hadSubject = true
		if grant.TaskID != query.TaskID {
			continue
		}
		hadTask = true
		if grant.State == GrantRevoked || grant.RevokedAt != nil {
			decision := Decision{Reason: ReasonRevoked, GrantID: grant.ID}
			return decision, e.appendAudit(ctx, e.decisionEvent(query, decision))
		}
		if !now.Before(grant.ExpiresAt.UTC()) || grant.State == GrantExpired {
			decision := Decision{Reason: ReasonExpired, GrantID: grant.ID}
			return decision, e.appendAudit(ctx, e.decisionEvent(query, decision))
		}
		if grant.State != GrantActive || canonicalResource(grant.Scope.Resource) != resource || !contains(grant.Scope.Actions, query.Action) {
			continue
		}
		decision := Decision{Allowed: true, Reason: ReasonAllowed, GrantID: grant.ID}
		if err := e.appendAudit(ctx, e.decisionEvent(query, decision)); err != nil {
			return Decision{}, err
		}
		return decision, nil
	}
	if !hadSubject {
		decision := Decision{Reason: ReasonSubjectMismatch}
		return decision, e.appendAudit(ctx, e.decisionEvent(query, decision))
	}
	if !hadTask {
		decision := Decision{Reason: ReasonTaskMismatch}
		return decision, e.appendAudit(ctx, e.decisionEvent(query, decision))
	}
	decision := Decision{Reason: ReasonDenied}
	return decision, e.appendAudit(ctx, e.decisionEvent(query, decision))
}

func (e *Engine) Revoke(ctx context.Context, request RevokeRequest) error {
	if !nonEmpty(request.ID) || !nonEmpty(request.Actor) {
		return ErrGrantNotFound
	}
	if e.authority == nil {
		return ErrDenied
	}
	if err := e.authority.AuthorizeRevoke(ctx, request); err != nil {
		return err
	}
	if e.repository == nil {
		return ErrCapability
	}
	if err := e.repository.RevokeGrant(ctx, request.ID, e.now().UTC()); err != nil {
		return err
	}
	return e.appendAudit(ctx, AuditEvent{ID: "capability.grant.revoked:" + request.ID, Type: "capability.grant.revoked", GrantID: request.ID, Reason: ReasonRevoked, Timestamp: e.now().UTC()})
}

func (e *Engine) appendAudit(ctx context.Context, event AuditEvent) error {
	if e.audit == nil {
		return nil
	}
	return e.audit.AppendCapabilityEvent(ctx, event)
}

func (e *Engine) decisionEvent(query Query, decision Decision) AuditEvent {
	resource := canonicalResource(query.Resource)
	hash := sha256.Sum256([]byte(query.Subject + "\x00" + query.TaskID + "\x00" + string(query.Kind) + "\x00" + resource + "\x00" + query.Action))
	eventType := "capability.authorize.denied"
	if decision.Allowed {
		eventType = "capability.authorize.allowed"
	}
	return AuditEvent{ID: eventType + ":" + hex.EncodeToString(hash[:]), Type: eventType, GrantID: decision.GrantID, Subject: query.Subject, TaskID: query.TaskID, Kind: query.Kind, Resource: resource, Reason: decision.Reason, Timestamp: e.now().UTC()}
}

func canonicalResource(resource string) string { return path.Clean(strings.TrimSpace(resource)) }

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
