package claude

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

func TestClaudeProbeSuccess(t *testing.T) {
	runner := &mockRunner{
		runFunc: func(ctx context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
			if strings.Contains(cmd.Args[0], "--version") {
				return adapter.ProcessResult{Stdout: []byte("1.0.0\n"), ExitCode: 0}, nil
			}
			return adapter.ProcessResult{Stdout: []byte("--print --output-format"), ExitCode: 0}, nil
		},
	}
	client := New("claude", runner)
	probe, err := client.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe failed: %v", err)
	}
	if probe.Name != "claude" || !probe.Available || probe.Version != "1.0.0" {
		t.Fatalf("unexpected probe result: %#v", probe)
	}
}

func TestClaudeRunSuccess(t *testing.T) {
	runner := &mockRunner{
		runFunc: func(ctx context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
			output := `{"session_id":"claude-sess-1","result":"Task completed successfully"}`
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
	client := New("claude", runner)
	result, err := client.Run(context.Background(), adapter.Request{
		TaskID:   "TASK-002",
		Title:    "Test Claude Task",
		Worktree: "/tmp/worktree",
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.Status != adapter.StatusSuccess {
		t.Fatalf("expected success status, got %s", result.Status)
	}
	if result.SessionID != "claude-sess-1" {
		t.Fatalf("expected session ID claude-sess-1, got %s", result.SessionID)
	}
	if result.FinalText != "Task completed successfully" {
		t.Fatalf("unexpected final text: %s", result.FinalText)
	}
}
