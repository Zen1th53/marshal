package app

import (
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/worker"
)

// withWorkerGoModuleCache gives a sandboxed provider worker the host's Go
// module cache read-only, with an offline Go environment, as checks have.
// Without it a worker's build tries to download every dependency, which a
// worker told not to use the network abandons as a violation.
func withWorkerGoModuleCache(binds []model.Bind, env []string) ([]model.Bind, []string) {
	bind, ok := worker.GoModuleCacheBind()
	if !ok {
		return binds, env
	}
	return append(binds, bind), append(env, worker.WorkerGoEnv()...)
}
