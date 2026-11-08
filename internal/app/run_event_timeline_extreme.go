//go:build !governed

package app

import (
	"context"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/store"
)

// MarshalRunTimeline narrows the admitted runtime read to one retained run.
// The binding is checked as identity; it grants no operator/model authority.
// Store keys use the admitted data project, never the binding nonce.
func (r *Runtime) MarshalRunTimeline(ctx context.Context, binding, runID string, limit int) ([]events.Event, error) {
	if r == nil || r.store == nil || binding == "" || binding != r.ProjectIdentity() {
		return nil, authz.ErrDenied
	}
	return r.store.RunEventTimeline(ctx, r.ProjectID(), runID, limit)
}

// MarshalImportRouting returns the committed import decisions, including the
// exact provider failure reason, without exposing unrelated event payloads.
func (r *Runtime) MarshalImportRouting(ctx context.Context, binding, runID string) ([]store.MarshalImportProvenance, error) {
	if r == nil || r.store == nil || binding == "" || binding != r.ProjectIdentity() {
		return nil, authz.ErrDenied
	}
	return r.store.MarshalImportProvenance(ctx, r.ProjectID(), runID)
}
