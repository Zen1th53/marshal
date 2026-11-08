//go:build governed

package app

import "github.com/Zen1th53/marshal/internal/execution"

func privateProcess05Harnesses(*Runtime) []execution.WorkerHarness                                  { return nil }
func configureRuntimeHarnessAdmission(*Runtime, *execution.EngineConfig, []execution.WorkerHarness) {}
func privatePreconfiguredExecutionService(*Runtime, *execution.Engine) (*ExecutionService, bool) {
	return nil, false
}
