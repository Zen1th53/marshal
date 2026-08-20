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

func TestOpenCodeModelSelectionPrecedence(t *testing.T) {
	runWith := func(client *Client) []string {
		var captured []string
		runner := &mockRunner{runFunc: func(_ context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
			captured = cmd.Args
			return adapter.ProcessResult{ExitCode: 0, Stdout: []byte("{}")}, nil
		}}
		client.runner = runner
		if _, err := client.Run(context.Background(), adapter.Request{
			TaskID: "TASK-MODEL", Title: "model", Worktree: "/tmp/wt",
		}); err != nil {
			t.Fatalf("Run: %v", err)
		}
		return captured
	}

	hasModelArg := func(args []string, want string) bool {
		for i := 0; i+1 < len(args); i++ {
			if args[i] == "-m" && args[i+1] == want {
				return true
			}
		}
		return false
	}

	// 1. Explicit model wins over environment.
	t.Setenv("MARSHAL_OPENCODE_MODEL", "env-model")
	if args := runWith(NewWithModel("opencode", nil, "explicit-model")); !hasModelArg(args, "explicit-model") {
		t.Fatalf("explicit model not forwarded: %v", args)
	}

	// 2. Environment fallback when no explicit model.
	t.Setenv("MARSHAL_OPENCODE_MODEL", "env-model")
	if args := runWith(New("opencode", nil)); !hasModelArg(args, "env-model") {
		t.Fatalf("env model not forwarded: %v", args)
	}

	// 3. No model and no env -> no -m flag.
	t.Setenv("MARSHAL_OPENCODE_MODEL", "")
	for _, a := range runWith(New("opencode", nil)) {
		if a == "-m" {
			t.Fatalf("unexpected -m flag with no model configured")
		}
	}
}
