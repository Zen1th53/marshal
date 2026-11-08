//go:build !governed

package cell

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"
)

// approvedCheckBackend binds callbacks to one immutable, approved check. The
// caller supplies the sandbox executor and the proof that its owned child has
// settled; the backend itself makes no process-isolation or lifecycle claim.
type approvedCheckBackend struct {
	spec    Spec
	command string
	execute func(context.Context, Handle, []string) (ExecResult, error)
	settle  func(context.Context, Handle) error
}

func NewApprovedCheckBackend(spec Spec, command string, execute func(context.Context, Handle, []string) (ExecResult, error), settle func(context.Context, Handle) error) (Backend, error) {
	bound, err := NewSpec(spec)
	if err != nil {
		return nil, err
	}
	if bound.Backend != BackendBubblewrap || bound.NetworkProfile != "deny-by-default" || bound.CPUQuota != 0 || bound.MemoryBytes != 0 || len(bound.SecretRefs) != 0 {
		return nil, fmt.Errorf("%w: approved checks require supported deny-by-default isolation", ErrBackendUnavailable)
	}
	if execute == nil || settle == nil || strings.TrimSpace(command) == "" || len(command) > 65536 || strings.ContainsRune(command, '\x00') {
		return nil, fmt.Errorf("%w: approved command and execution/settlement callbacks required", ErrPrepareFailed)
	}
	return &approvedCheckBackend{spec: bound, command: command, execute: execute, settle: settle}, nil
}

// CellIdentity matches the existing manager identity for the exact specification.
func CellIdentity(spec Spec) (CellID, error) {
	bound, err := NewSpec(spec)
	if err != nil {
		return "", err
	}
	return cellIDFor(bound), nil
}

func CellSpecDigest(spec Spec) (SpecDigest, error) {
	bound, err := NewSpec(spec)
	if err != nil {
		return "", err
	}
	return digestSpec(bound), nil
}

func (b *approvedCheckBackend) Prepare(ctx context.Context, spec Spec) (Handle, error) {
	if err := ctx.Err(); err != nil {
		return Handle{}, err
	}
	if !reflect.DeepEqual(spec, b.spec) {
		return Handle{}, fmt.Errorf("%w: approved specification changed", ErrScopeEscape)
	}
	return Handle{ID: cellIDFor(b.spec), TaskID: b.spec.TaskID, Backend: b.spec.Backend, Workspace: b.spec.Workspace, CreatedAt: time.Now().UTC()}, nil
}

func (b *approvedCheckBackend) validateHandle(h Handle) error {
	if err := h.Validate(); err != nil {
		return err
	}
	if h.ID != cellIDFor(b.spec) || h.TaskID != b.spec.TaskID || h.Backend != b.spec.Backend || h.Workspace != b.spec.Workspace {
		return fmt.Errorf("%w: handle differs from approved specification", ErrScopeEscape)
	}
	return nil
}

func (b *approvedCheckBackend) Exec(ctx context.Context, h Handle, req ExecRequest) (ExecResult, error) {
	if err := ctx.Err(); err != nil {
		return ExecResult{}, err
	}
	if err := b.validateHandle(h); err != nil {
		return ExecResult{}, err
	}
	argv := []string{"/bin/sh", "-c", b.command}
	if len(req.Env) != 0 || !reflect.DeepEqual(req.Command, argv) {
		return ExecResult{}, fmt.Errorf("%w: request differs from approved check", ErrScopeEscape)
	}
	return b.execute(ctx, h, argv)
}

func (b *approvedCheckBackend) Destroy(ctx context.Context, h Handle) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := b.validateHandle(h); err != nil {
		return err
	}
	if err := b.settle(ctx, h); err != nil {
		return fmt.Errorf("%w: %w", ErrCleanupFailed, err)
	}
	return nil
}
