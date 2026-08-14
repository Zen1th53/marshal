package opencode

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/model"
)

type mockRunner struct {
	runFunc func(ctx context.Context, cmd adapter.Command) (adapter.ProcessResult, error)
}

func (m *mockRunner) Run(ctx context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
	return m.runFunc(ctx, cmd)
}

func TestOpenCodeProbeSuccess(t *testing.T) {
	runner := &mockRunner{
		runFunc: func(ctx context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
			if strings.Contains(cmd.Args[0], "--version") {
				return adapter.ProcessResult{Stdout: []byte("0.1.0\n"), ExitCode: 0}, nil
			}
			return adapter.ProcessResult{Stdout: []byte("run --format json"), ExitCode: 0}, nil
		},
	}
	client := New("opencode", runner)
	probe, err := client.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe failed: %v", err)
	}
	if probe.Name != "opencode" || !probe.Available || probe.Version != "0.1.0" {
		t.Fatalf("unexpected probe result: %#v", probe)
	}
}

func TestOpenCodeRunSuccess(t *testing.T) {
	runner := &mockRunner{
		runFunc: func(ctx context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
			output := `{"session_id":"opencode-sess-1","result":"Task completed successfully"}`
			return adapter.ProcessResult{
				Stdout:    []byte(output),
				Stderr:    []byte(""),
				ExitCode:  0,
				StartedAt: time.Now().Add(-time.Second),
				EndedAt:   time.Now(),
				Isolation: model.IsolationCapability{Level: model.IsolationBwrap, Available: true},
			}, nil
		},
	}
	client := New("opencode", runner)
	result, err := client.Run(context.Background(), adapter.Request{
		TaskID:   "TASK-003",
		Title:    "Test OpenCode Task",
		Worktree: "/tmp/worktree",
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.Status != adapter.StatusSuccess {
		t.Fatalf("expected success status, got %s", result.Status)
	}
	if result.SessionID != "opencode-sess-1" {
		t.Fatalf("expected session ID opencode-sess-1, got %s", result.SessionID)
	}
	if result.FinalText != "Task completed successfully" {
		t.Fatalf("unexpected final text: %s", result.FinalText)
	}
}
