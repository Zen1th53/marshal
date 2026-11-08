package app

import (
	"context"
	"fmt"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

// AdmitMarshalResume must precede resume's operation replay or worker launch.
// It checks the live owner, then records any compatibility stop for readback.
func (r *Runtime) AdmitMarshalResume(ctx context.Context, runID string) (store.MarshalCompatibility, error) {
	if r == nil || r.store == nil {
		return store.MarshalCompatibility{}, model.ErrUnavailable
	}
	if err := r.requireLifecycleOwner(); err != nil {
		return store.MarshalCompatibility{}, err
	}
	if err := r.store.PauseIncompatibleMarshalRuns(ctx, r.ProjectID()); err != nil {
		return store.MarshalCompatibility{}, err
	}
	c, err := r.store.MarshalRunCompatibility(ctx, r.ProjectID(), runID)
	if err != nil {
		return c, err
	}
	if !c.Resumable {
		return c, fmt.Errorf("%w: %s; %s", model.ErrConflict, c.Reason, c.RecoveryAction)
	}
	return c, nil
}
