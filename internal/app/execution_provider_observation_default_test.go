//go:build governed

package app

import (
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"testing"
)

func assertNativeProviderObservation(t *testing.T, task execution.TaskExecution, result execution.TaskResult) {
}

func assertGovernedProviderEvidence(t *testing.T, run *execution.ExecutionRun) {}

func process05ObservedTestEvidence(execution.TaskExecution) []execution.ExecutionEvidence { return nil }
func assertProcess05ObservedTestHandIn(*testing.T, *MarshalService, marshal.Run)          {}
