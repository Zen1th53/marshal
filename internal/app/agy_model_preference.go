package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter/antigravity"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/project"
	"github.com/Zen1th53/marshal/internal/worker"
)

// AgyModelPreference is the owner's choice for future agy runs: usually a
// selector (auto, best, fast, a family) rather than a model.
func (r *Runtime) AgyModelPreference(ctx context.Context) (model.ExecutionModelPreference, error) {
	if r == nil || r.store == nil {
		return model.ExecutionModelPreference{}, fmt.Errorf("runtime store is unavailable")
	}
	return r.store.GetExecutionModelPreference(ctx, localProjectID, "agy")
}

// SetAgyModelPreference stores a selector or an exact model for agy. It is
// checked against what the signed-in agy offers now, but the selector itself
// is stored, so each later probe picks the best model offered at that time.
func (r *Runtime) SetAgyModelPreference(ctx context.Context, selector string, expectedRevision int64) (model.ExecutionModelPreference, error) {
	if r == nil || r.store == nil {
		return model.ExecutionModelPreference{}, fmt.Errorf("runtime store is unavailable")
	}
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return model.ExecutionModelPreference{}, fmt.Errorf("%w: agy model or selector is required", model.ErrInvalid)
	}
	if antigravity.IsSelector(selector) {
		selector = strings.ToLower(selector)
	}
	binary, err := project.FindBinary("agy")
	if err != nil {
		return model.ExecutionModelPreference{}, fmt.Errorf("%w: agy CLI is missing", model.ErrUnavailable)
	}
	models, err := antigravity.DiscoverModels(ctx, binary, worker.New(60*time.Second, 2*time.Second, 1<<20))
	if err != nil {
		return model.ExecutionModelPreference{}, err
	}
	if _, err := antigravity.Resolve(selector, models); err != nil {
		return model.ExecutionModelPreference{}, err
	}
	return r.store.SetExecutionModelPreference(ctx, model.ExecutionModelPreference{
		ProjectID: localProjectID,
		Adapter:   "agy",
		Model:     selector,
	}, expectedRevision)
}
