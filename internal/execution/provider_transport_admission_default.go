//go:build governed

package execution

import "context"

type providerTransportAdmission struct{}

func (*Engine) ownsProviderTransportAdmission(TaskResult, ExecutionRun, TaskExecution) bool {
	return false
}

func (*Engine) releaseProviderTransportReservation(context.Context, TaskResult, ExecutionRun, TaskExecution) error {
	return nil
}
