package worker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/processgroup"
	"github.com/Zen1th53/marshal/internal/sandbox"
)

type Manager struct {
	timeout     time.Duration
	grace       time.Duration
	outputLimit int
}

func New(timeout, grace time.Duration, outputLimit int) *Manager {
	return &Manager{timeout: timeout, grace: grace, outputLimit: outputLimit}
}

func (m *Manager) Run(ctx context.Context, command adapter.Command) (adapter.ProcessResult, error) {
	if command.Path == "" || m.outputLimit <= 0 {
		return adapter.ProcessResult{}, fmt.Errorf("invalid worker command or output limit")
	}
	runCtx := ctx
	cancel := func() {}
	if m.timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, m.timeout)
	}
	defer cancel()

	cmd := exec.Command(command.Path, command.Args...)
	cmd.Dir = command.Dir
	if command.Env != nil {
		cmd.Env = command.Env
	}
	cmd.Stdin = bytes.NewReader(command.Stdin)
	stdout := &limitedBuffer{limit: m.outputLimit, observe: func(p []byte) {
		if command.OutputObserver != nil {
			command.OutputObserver("stdout", p)
		}
	}}
	stderr := &limitedBuffer{limit: m.outputLimit, observe: func(p []byte) {
		if command.OutputObserver != nil {
			command.OutputObserver("stderr", p)
		}
	}}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := processgroup.Wrap(cmd); err != nil {
		return adapter.ProcessResult{}, err
	}

	result := adapter.ProcessResult{StartedAt: time.Now().UTC(), Isolation: model.IsolationCapability{
		Level: model.IsolationProcessOnly, Available: true, Process: true,
		Reason: "task-scoped process without strong filesystem or network isolation",
	}}
	observerCtx, stopObserver := context.WithCancel(runCtx)
	defer stopObserver()
	var observe func() error
	if command.Supervised {
		var err error
		observe, err = sandbox.AttachSupervisor(observerCtx, cmd, command.SupervisorSocket, command.Refusal)
		if err != nil {
			return adapter.ProcessResult{}, err
		}
	}
	if err := cmd.Start(); err != nil {
		if observe != nil {
			stopObserver()
			_ = observe()
		}
		return adapter.ProcessResult{}, fmt.Errorf("start worker process: %w", err)
	}
	observerDone := make(chan error, 1)
	if observe != nil {
		go func() {
			err := observe()
			if err != nil && observerCtx.Err() == nil {
				_ = processgroup.Stop(cmd)
			}
			observerDone <- err
		}()
	}
	wait := make(chan error, 1)
	go func() {
		wait <- cmd.Wait()
	}()

	var ticker *time.Ticker
	var heartbeat <-chan time.Time
	if command.Heartbeat != nil && command.HeartbeatInterval > 0 {
		ticker = time.NewTicker(command.HeartbeatInterval)
		defer ticker.Stop()
		heartbeat = ticker.C
	}

	var waitErr error
	finished := false
	for !finished {
		select {
		case waitErr = <-wait:
			finished = true
		case <-heartbeat:
			command.Heartbeat()
		case <-runCtx.Done():
			result.TimedOut = errors.Is(runCtx.Err(), context.DeadlineExceeded)
			result.Cancelled = !result.TimedOut
			_ = processgroup.Stop(cmd)
			timer := time.NewTimer(m.grace)
			select {
			case waitErr = <-wait:
				if !timer.Stop() {
					<-timer.C
				}
			case <-timer.C:
				// Keep the subreaper alive until all descendants are reaped.
				_ = processgroup.Stop(cmd)
				waitErr = <-wait
			}
			finished = true
		}
	}
	stopObserver()
	if observe != nil {
		if err := <-observerDone; err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			return adapter.ProcessResult{}, err
		}
	}
	result.EndedAt = time.Now().UTC()
	result.Stdout = stdout.Bytes()
	result.Stderr = stderr.Bytes()
	result.OutputTruncated = stdout.Truncated() || stderr.Truncated()
	result.ExitCode = exitCode(cmd, waitErr)
	if waitErr != nil {
		var exitError *exec.ExitError
		if !errors.As(waitErr, &exitError) {
			return adapter.ProcessResult{}, fmt.Errorf("wait for worker process: %w", waitErr)
		}
	}
	return result, nil
}

func exitCode(cmd *exec.Cmd, waitErr error) int {
	if cmd.ProcessState == nil {
		return -1
	}
	code := cmd.ProcessState.ExitCode()
	if code >= 0 {
		return code
	}
	if status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal())
	}
	if waitErr != nil {
		return 1
	}
	return 0
}

type limitedBuffer struct {
	mu        sync.Mutex
	data      []byte
	limit     int
	truncated bool
	observe   func([]byte)
}

func (b *limitedBuffer) Write(value []byte) (int, error) {
	if b.observe != nil {
		b.observe(value)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.limit - len(b.data)
	if remaining > 0 {
		count := len(value)
		if count > remaining {
			count = remaining
		}
		b.data = append(b.data, value[:count]...)
	}
	if len(value) > remaining {
		b.truncated = true
	}
	return len(value), nil
}

func (b *limitedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.data...)
}

func (b *limitedBuffer) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}
