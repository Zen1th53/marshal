//go:build governed

package app

import (
	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
)

func process05ProviderObservation(execution.TaskExecution, adapter.Result, *execution.NativeTurnBinding, string) *execution.ProviderObservation {
	return nil
}
func marshalProcess05Observations(*execution.ExecutionRun, execution.TaskExecution) ([]marshal.CommandRecord, error) {
	return nil, nil
}
