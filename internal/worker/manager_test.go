package worker

import (
	"context"
	"os/exec"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/slaves/internal/adapter"
	"github.com/Zen1th53/slaves/internal/model"
)

type captureProcessRunner struct {
	command adapter.Command
}

func (r *captureProcessRunner) Run(_ context.Context, command adapter.Command) (adapter.ProcessResult, error) {
	r.command = command
	return adapter.ProcessResult{ExitCode: 0}, nil
}

type captureWrapper struct {
	command []string
}

func (w *captureWrapper) Wrap(_ model.SandboxRequest, command []string) (model.CommandSpec, error) {
	w.command = append([]string(nil), command...)
	return model.CommandSpec{
		Path: "/sbin/bwrap", Args: []string{"--", command[0]},
		Env: []string{"HOME=/home/slaves"}, Dir: "/task",
		Isolation: model.IsolationCapability{Level: model.IsolationBwrap, Available: true},
	}, nil
}

func TestManagerReturnsNonzeroExitAsEvidenceNotInfrastructureError(t *testing.T) {
	manager := New(2*time.Second, 100*time.Millisecond, 1<<20)
	result, err := manager.Run(context.Background(), adapter.Command{
		Path: "/bin/sh", Args: []string{"-c", "printf out; printf err >&2; exit 7"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 7 || string(result.Stdout) != "out" || string(result.Stderr) != "err" {
		t.Fatalf("result = %#v", result)
	}
	if result.Isolation.Level != model.IsolationProcessOnly || !result.Isolation.Process {
		t.Fatalf("process isolation = %#v", result.Isolation)
	}
}

func TestManagerTimeoutTerminatesProcessGroupAndKeepsHeartbeatEvidence(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh unavailable")
	}
	manager := New(120*time.Millisecond, 100*time.Millisecond, 1<<20)
	var heartbeats atomic.Int32
	started := time.Now()
	result, err := manager.Run(context.Background(), adapter.Command{
		Path:              "/bin/sh",
		Args:              []string{"-c", "trap '' TERM; sleep 30 & wait"},
		Heartbeat:         func() { heartbeats.Add(1) },
		HeartbeatInterval: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.TimedOut || result.ExitCode == 0 {
		t.Fatalf("result = %#v", result)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("timeout took %v", elapsed)
	}
	if heartbeats.Load() == 0 {
		t.Fatal("no heartbeat evidence captured")
	}
}

func TestManagerRejectsOutputOverflowExplicitly(t *testing.T) {
	manager := New(time.Second, 100*time.Millisecond, 4)
	result, err := manager.Run(context.Background(), adapter.Command{
		Path: "/bin/sh", Args: []string{"-c", "printf 123456789"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.OutputTruncated || string(result.Stdout) != "1234" {
		t.Fatalf("result = %#v", result)
	}
}

func TestSandboxedRunnerWrapsExactCommandAndPreservesProcessInputs(t *testing.T) {
	process := &captureProcessRunner{}
	wrapper := &captureWrapper{}
	runner := NewSandboxed(process, wrapper, model.SandboxRequest{Worktree: "/task"})
	heartbeat := func() {}
	result, err := runner.Run(context.Background(), adapter.Command{
		Path: "/usr/bin/codex", Args: []string{"exec"}, Stdin: []byte("prompt"),
		Heartbeat: heartbeat, HeartbeatInterval: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(wrapper.command, []string{"/usr/bin/codex", "exec"}) {
		t.Fatalf("wrapped command = %#v", wrapper.command)
	}
	if process.command.Path != "/sbin/bwrap" || string(process.command.Stdin) != "prompt" ||
		process.command.Heartbeat == nil || process.command.HeartbeatInterval != time.Second {
		t.Fatalf("process command = %#v", process.command)
	}
	if result.Isolation.Level != model.IsolationBwrap {
		t.Fatalf("result isolation = %#v", result.Isolation)
	}
}
