//go:build governed

package cli

import (
	"context"
	"fmt"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
)

func (c command) draftModelPlan(context.Context, *app.Runtime, []string) error {
	return fmt.Errorf("%w: model plan drafting is unavailable in this build", model.ErrUnavailable)
}
