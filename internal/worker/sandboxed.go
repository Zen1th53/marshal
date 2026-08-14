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
}

func NewSandboxed(process adapter.ProcessRunner, wrapper commandWrapper, request model.SandboxRequest) adapter.ProcessRunner {
	return &sandboxedRunner{process: process, wrapper: wrapper, request: request}
}

func (r *sandboxedRunner) Run(ctx context.Context, command adapter.Command) (adapter.ProcessResult, error) {
	argv := append([]string{command.Path}, command.Args...)
	spec, err := r.wrapper.Wrap(r.request, argv)
	if err != nil {
		return adapter.ProcessResult{}, err
	}
	result, err := r.process.Run(ctx, adapter.Command{
		Path: spec.Path, Args: spec.Args, Env: spec.Env, Dir: spec.Dir,
		Stdin: command.Stdin, Heartbeat: command.Heartbeat,
		HeartbeatInterval: command.HeartbeatInterval,
	})
	result.Isolation = spec.Isolation
	return result, err
}
