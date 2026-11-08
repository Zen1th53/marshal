//go:build !governed

package worker

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/Zen1th53/marshal/internal/processgroup"
	"github.com/Zen1th53/marshal/internal/workerterminal"
)

func wrapWorkerProcess(ctx context.Context, cmd *exec.Cmd) (func() error, func(), error) {
	if !workerterminal.HeldCellStart(ctx) {
		return func() error { return nil }, func() {}, processgroup.Wrap(cmd)
	}
	release, cleanup, err := processgroup.WrapHeld(cmd)
	if err != nil {
		return nil, nil, err
	}
	return func() error {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("private cell release refused: %w", err)
		}
		return release()
	}, cleanup, nil
}
