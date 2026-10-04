package worker

import (
	"context"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/model"
)

type commandWrapper interface {
	Wrap(model.SandboxRequest, []string) (model.CommandSpec, error)
}

type sandboxedRunner struct {
	process adapter.ProcessRunner
	wrapper commandWrapper
	request model.SandboxRequest
	observe func(string, []byte) bool
	check   func(context.Context, *adapter.ProcessResult) error
}

func NewSandboxed(process adapter.ProcessRunner, wrapper commandWrapper, request model.SandboxRequest) adapter.ProcessRunner {
	return &sandboxedRunner{process: process, wrapper: wrapper, request: request}
}

// NewGuardedSandboxed inspects raw output and changes before the adapter sees them.
func NewGuardedSandboxed(process adapter.ProcessRunner, wrapper commandWrapper, request model.SandboxRequest, observe func(string, []byte) bool, check func(context.Context, *adapter.ProcessResult) error) adapter.ProcessRunner {
	return &sandboxedRunner{process: process, wrapper: wrapper, request: request, observe: observe, check: check}
}

func (r *sandboxedRunner) Run(ctx context.Context, command adapter.Command) (adapter.ProcessResult, error) {
	argv := append([]string{command.Path}, command.Args...)
	spec, err := r.wrapper.Wrap(r.request, argv)
	if err != nil {
		return adapter.ProcessResult{}, err
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	result, err := r.process.Run(runCtx, adapter.Command{
		OutputObserver: func(stream string, data []byte) {
			if r.observe != nil && r.observe(stream, data) {
				cancel()
			}
			if command.OutputObserver != nil {
				command.OutputObserver(stream, data)
			}
		},
		Path: spec.Path, Args: spec.Args, Env: spec.Env, Dir: spec.Dir,
		Stdin: command.Stdin, Heartbeat: command.Heartbeat,
		HeartbeatInterval: command.HeartbeatInterval,
	})
	result.Isolation = spec.Isolation
	if r.check != nil {
		if checkErr := r.check(context.WithoutCancel(ctx), &result); checkErr != nil {
			result.ExitCode = -1
			return result, checkErr
		}
	}
	return result, err
}
