//go:build governed || !unix

package app

import (
	"context"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/policy"
)

func (r *Runtime) workerMCPContext(ctx context.Context, taskID, agentID, provider string, attempt *execution.TaskExecution) (context.Context, func(), error) {
	return r.leaseWorkerMCPContext(ctx, taskID, attempt)
}
func (r *Runtime) authorizeBoundWorkerSourceRead(context.Context, string, string) (bool, error) {
	return false, nil
}
func (r *Runtime) authorizeWorkerReadPreflight(context.Context, string, string, string, policy.Action, policy.Resource) (bool, error) {
	return false, nil
}

func (r *Runtime) boundWorkerKnowledgeRoot(context.Context) string { return "" }
