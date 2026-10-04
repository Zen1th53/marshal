package worker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/sandbox"
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
	refusal func(context.Context, sandbox.Refusal) error
}

func NewSandboxed(process adapter.ProcessRunner, wrapper commandWrapper, request model.SandboxRequest) adapter.ProcessRunner {
	return &sandboxedRunner{process: process, wrapper: wrapper, request: request}
}

// NewGuardedSandboxed inspects raw output and changes before the adapter sees them.
func NewGuardedSandboxed(process adapter.ProcessRunner, wrapper commandWrapper, request model.SandboxRequest, observe func(string, []byte) bool, check func(context.Context, *adapter.ProcessResult) error, refusal ...func(context.Context, sandbox.Refusal) error) adapter.ProcessRunner {
	runner := &sandboxedRunner{process: process, wrapper: wrapper, request: request, observe: observe, check: check}
	if len(refusal) > 0 {
		runner.refusal = refusal[0]
	}
	return runner
}

func NewObservedSandboxed(process adapter.ProcessRunner, wrapper commandWrapper, request model.SandboxRequest, refusal func(context.Context, sandbox.Refusal) error) adapter.ProcessRunner {
	return &sandboxedRunner{process: process, wrapper: wrapper, request: request, refusal: refusal}
}

func (r *sandboxedRunner) Run(ctx context.Context, command adapter.Command) (adapter.ProcessResult, error) {
	argv := append([]string{command.Path}, command.Args...)
	request := r.request
	if r.refusal == nil {
		if _, actual := r.process.(*Manager); actual {
			return adapter.ProcessResult{}, fmt.Errorf("socket refusal observer required")
		}
		// Injected runners have no subprocess authority, like injected adapters.
	} else {
		var exe string
		var err error
		argv, exe, err = sandbox.SupervisedArgv(argv)
		if err != nil {
			return adapter.ProcessResult{}, err
		}
		dir, err := os.MkdirTemp("", "marshal-observer-")
		if err != nil {
			return adapter.ProcessResult{}, err
		}
		defer os.RemoveAll(dir)
		request.SupervisorSocket = filepath.Join(dir, "observer.sock")
		request.Supervised = true
		request.SupervisorBinary = exe
	}
	spec, err := r.wrapper.Wrap(request, argv)
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
		Supervised: request.Supervised, SupervisorSocket: request.SupervisorSocket, Refusal: r.refusal,
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
