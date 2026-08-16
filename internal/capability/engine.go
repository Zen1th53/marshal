package capability

import (
	"context"
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
}

func NewEngine(repository GrantRepository, now func() time.Time) *Engine {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Engine{repository: repository, now: now}
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
	return grant, nil
}

func (e *Engine) Authorize(ctx context.Context, query Query) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}
	if !nonEmpty(query.Subject) || !nonEmpty(query.TaskID) || !query.Kind.Valid() || !nonEmpty(query.Resource) || !nonEmpty(query.Action) {
		return Decision{Reason: ReasonInvalidScope}, nil
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
			return Decision{Reason: ReasonRevoked, GrantID: grant.ID}, nil
		}
		if !now.Before(grant.ExpiresAt.UTC()) || grant.State == GrantExpired {
			return Decision{Reason: ReasonExpired, GrantID: grant.ID}, nil
		}
		if grant.State != GrantActive || canonicalResource(grant.Scope.Resource) != resource || !contains(grant.Scope.Actions, query.Action) {
			continue
		}
		return Decision{Allowed: true, Reason: ReasonAllowed, GrantID: grant.ID}, nil
	}
	if !hadSubject {
		return Decision{Reason: ReasonSubjectMismatch}, nil
	}
	if !hadTask {
		return Decision{Reason: ReasonTaskMismatch}, nil
	}
	return Decision{Reason: ReasonDenied}, nil
}

func (e *Engine) Revoke(ctx context.Context, id string) error {
	if !nonEmpty(id) {
		return ErrGrantNotFound
	}
	if e.repository == nil {
		return ErrCapability
	}
	return e.repository.RevokeGrant(ctx, id, e.now().UTC())
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
