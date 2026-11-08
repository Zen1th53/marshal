//go:build governed

package app

import (
	"context"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
)

func (r *Runtime) marshalProcess05Provenance(context.Context, marshal.Run, *execution.ExecutionRun, execution.TaskExecution) ([]marshal.CommandRecord, error) {
	return nil, nil
}
