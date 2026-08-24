package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
)

// TaskMemoryGrantRequest binds an existing agent to an existing task. The
// policy digest is caller-supplied evidence of the policy decision authorizing
// the grant; the memory service does not invent policy authority.
type TaskMemoryGrantRequest struct {
	TaskID       string `json:"task_id"`
	PrincipalID  string `json:"principal_id"`
	Role         string `json:"role,omitempty"`
	PolicyDigest string `json:"policy_digest"`
}

func (s *MemoryService) GrantTaskAccess(ctx context.Context, principal authz.Principal, req TaskMemoryGrantRequest) (authz.RoleBinding, error) {
	if err := ctx.Err(); err != nil {
		return authz.RoleBinding{}, err
	}
	if s == nil || s.store == nil {
		return authz.RoleBinding{}, fmt.Errorf("%w: memory store unavailable", model.ErrUnavailable)
	}
	if !principalHasAuthority(principal, authz.AuthorityPolicyAdmin) {
		return authz.RoleBinding{}, authz.ErrUnauthorized
	}
	req.TaskID = strings.TrimSpace(req.TaskID)
	req.PrincipalID = strings.TrimSpace(req.PrincipalID)
	req.Role = strings.TrimSpace(req.Role)
	if req.Role == "" {
		req.Role = "task-member"
	}
	if req.TaskID == "" || req.PrincipalID == "" || strings.TrimSpace(req.PolicyDigest) == "" {
		return authz.RoleBinding{}, fmt.Errorf("%w: task_id, principal_id, and policy_digest are required", model.ErrInvalid)
	}
	if _, err := s.store.GetTask(ctx, req.TaskID); err != nil {
		return authz.RoleBinding{}, fmt.Errorf("read task for memory grant: %w", err)
	}
	agents, err := s.store.ListAgents(ctx)
	if err != nil {
		return authz.RoleBinding{}, fmt.Errorf("list agents for memory grant: %w", err)
	}
	found := false
	for _, agent := range agents {
		if agent.ID == req.PrincipalID && agent.Status != model.AgentDisabled {
			found = true
			break
		}
	}
	if !found {
		return authz.RoleBinding{}, fmt.Errorf("%w: active agent %s", model.ErrNotFound, req.PrincipalID)
	}
	sum := sha256.Sum256([]byte(req.TaskID + "\x00" + req.PrincipalID))
	bindingID := "MEM-GRANT-" + hex.EncodeToString(sum[:])[:24]
	if existing, getErr := s.store.GetRoleBinding(ctx, bindingID); getErr == nil {
		if existing.PrincipalID == req.PrincipalID && existing.ScopeID == req.TaskID && existing.Role == req.Role &&
			existing.BoundBy == principal.ID && existing.PolicyDigest == req.PolicyDigest && existing.RevokedAt == nil {
			return existing, nil
		}
		return authz.RoleBinding{}, fmt.Errorf("%w: task memory grant is immutable or revoked", model.ErrConflict)
	} else if !errors.Is(getErr, model.ErrNotFound) {
		return authz.RoleBinding{}, getErr
	}
	binding := authz.RoleBinding{
		ID: bindingID, PrincipalID: req.PrincipalID,
		Role: req.Role, ScopeID: req.TaskID, BoundBy: principal.ID, BoundAt: time.Now().UTC(),
		PolicyDigest: req.PolicyDigest,
	}
	if err := s.store.PutRoleBinding(ctx, binding); err != nil {
		return authz.RoleBinding{}, err
	}
	return binding, nil
}

func (s *MemoryService) RevokeTaskAccess(ctx context.Context, principal authz.Principal, bindingID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.store == nil {
		return fmt.Errorf("%w: memory store unavailable", model.ErrUnavailable)
	}
	if !principalHasAuthority(principal, authz.AuthorityPolicyAdmin) {
		return authz.ErrUnauthorized
	}
	bindingID = strings.TrimSpace(bindingID)
	if bindingID == "" {
		return fmt.Errorf("%w: binding_id is required", model.ErrInvalid)
	}
	_, err := s.store.GetRoleBinding(ctx, bindingID)
	if err != nil {
		return err
	}
	return s.store.RevokeRoleBinding(ctx, bindingID, time.Now().UTC())
}
