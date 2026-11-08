//go:build governed

package worker

import (
	"context"
	"os/exec"

	"github.com/Zen1th53/marshal/internal/processgroup"
)

func wrapWorkerProcess(_ context.Context, cmd *exec.Cmd) (func() error, func(), error) {
	return func() error { return nil }, func() {}, processgroup.Wrap(cmd)
}
