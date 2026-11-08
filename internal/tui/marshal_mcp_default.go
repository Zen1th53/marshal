//go:build governed

package tui

import (
	"context"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/model"
)

func configureWorkerDriverExtreme(d driver.Driver, _ string, _ *activeTmuxAgent) driver.Driver {
	return d
}

// nativeMarshalAuthority is false in the default edition: interactive
// Marshal sessions use the token-scoped session memory server.
const nativeMarshalAuthority = false

// Community has no model contract acceptance; the owner's click is required.
func (w *Workspace) recordProjectContractInstruction(context.Context, string, string, string, time.Time) (app.OperatorInstructionBinding, bool, error) {
	return app.OperatorInstructionBinding{}, false, model.ErrPolicyDenied
}
