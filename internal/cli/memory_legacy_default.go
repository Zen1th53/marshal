//go:build governed

package cli

import (
	"context"
	"fmt"

	"github.com/Zen1th53/marshal/internal/model"
)

func (c command) memoryLegacyMigration(_ context.Context, args []string) (bool, error) {
	if len(args) > 0 && (args[0] == "migrate-legacy" || args[0] == "rollback-migration") {
		return true, fmt.Errorf("%w: private memory migration requires the Marshal build", model.ErrUnavailable)
	}
	return false, nil
}
