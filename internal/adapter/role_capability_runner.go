package adapter

import (
	"context"
	"strings"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/capability"
)

// RoleCapabilityRunner composes T04 role authority with the canonical T01
// scoped capability check before any provider process is invoked.
type RoleCapabilityRunner struct {
	base      ProcessRunner
	principal authz.Principal
	taskID    capability.TaskID
	authority authz.Authority
	broker    capability.Broker
}

func NewRoleCapabilityRunner(base ProcessRunner, principal authz.Principal, taskID string, authority authz.Authority, broker capability.Broker) ProcessRunner {
	return &RoleCapabilityRunner{base: base, principal: principal, taskID: capability.TaskID(taskID), authority: authority, broker: broker}
}

func (r *RoleCapabilityRunner) Run(ctx context.Context, command Command) (ProcessResult, error) {
	if r == nil || r.base == nil || r.broker == nil || strings.TrimSpace(command.Path) == "" {
		return ProcessResult{}, capability.ErrDenied
	}
	query := capability.Query{Subject: capability.SubjectID(r.principal.ID), TaskID: r.taskID, Kind: capability.KindShellExec, Resource: command.Path, Action: "execute"}
	decision, err := authz.CanWithCapability(ctx, r.principal, r.authority, command.Path, query, r.broker)
	if err != nil {
		return ProcessResult{}, err
	}
	if !decision.Allowed {
		return ProcessResult{}, capability.ErrDenied
	}
	return r.base.Run(ctx, command)
}
