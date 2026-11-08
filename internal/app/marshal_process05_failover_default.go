//go:build governed

package app

import (
	"context"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
)

func (r *Runtime) prepareProcess05ProviderFailover(ctx context.Context, s *MarshalService, run marshal.Run, req driver.Request, p05 *execution.ExecutionRun) (*execution.ExecutionRun, error) {
	return p05, nil
}
func (r *Runtime) process05ProviderFailure(ctx context.Context, req driver.Request, completed *execution.ExecutionRun, err error) error {
	return err
}

func (r *Runtime) prepareProcess05ProviderSwitch(_ context.Context, _ *MarshalService, _ marshal.Run, _ driver.Request, p05 *execution.ExecutionRun) (*execution.ExecutionRun, error) {
	return p05, nil
}
