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

func TestCurrentOpenCodeStreamTextAndSession(t *testing.T) {
	var result adapter.Result
	parseOpenCodeOutput([]byte(`{"type":"step_start","sessionID":"ses-current","part":{"type":"step-start"}}
{"type":"text","sessionID":"ses-current","part":{"type":"text","text":"MARSHAL_"}}
{"type":"text","sessionID":"ses-current","part":{"type":"text","text":"PROBE_OK"}}`), &result)
	if result.SessionID != "ses-current" || result.FinalText != "MARSHAL_PROBE_OK" {
		t.Fatalf("current stream incorrectly parsed: %#v", result)
	}
}

func TestOpenCodeProbeRefusesBootstrapVersionMismatch(t *testing.T) {
	runner := &mockRunner{runFunc: func(context.Context, adapter.Command) (adapter.ProcessResult, error) {
		return adapter.ProcessResult{Stdout: []byte("1.18.17\n")}, nil
	}}
	client := NewWithRequiredVersion("opencode", runner, "model", "1.18.16")
	if _, err := client.Probe(context.Background()); err == nil {
		t.Fatal("mismatched CLI/bootstrap version accepted")
	}
	runner.runFunc = func(context.Context, adapter.Command) (adapter.ProcessResult, error) {
		return adapter.ProcessResult{Stdout: []byte("1.18.16\n")}, nil
	}
	if _, err := client.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestOpenCodeRunRefusesSuccessfulExitWithProviderError(t *testing.T) {
	runner := &mockRunner{runFunc: func(context.Context, adapter.Command) (adapter.ProcessResult, error) {
		return adapter.ProcessResult{Stdout: []byte(`{"type":"error","sessionID":"ses-failed","error":{"name":"APIError","data":{"message":"provider unavailable"}}}`), ExitCode: 0}, nil
	}}
	result, err := New("opencode", runner).Run(context.Background(), adapter.Request{TaskID: "TASK-ERROR", Title: "provider refused", Worktree: "/tmp/worktree"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != adapter.StatusFailure || result.SessionID != "ses-failed" {
		t.Fatalf("provider error mislabeled successful: %#v", result)
	}
}

const installedRunHelp = `USAGE
  opencode run [flags] [<message...>]

FLAGS
  --standalone            Run with a private server instead of the background service
  --session, -s string    Session ID to continue, or to create if it does not exist
  --model, -m string      Model to use in the format provider/model#variant
  --format choice         Output format (choices: default, json)
`

func TestOpenCodePinnedProbeAcceptsDifferentVersionOnlyWithMatchingContract(t *testing.T) {
	var runArgs []string
	runner := &mockRunner{runFunc: func(_ context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
		switch {
		case len(cmd.Args) == 1 && cmd.Args[0] == "--version":
			return adapter.ProcessResult{Stdout: []byte("opencode v2.1.4\n")}, nil
		case len(cmd.Args) == 2 && cmd.Args[0] == "run" && cmd.Args[1] == "--help":
			return adapter.ProcessResult{Stdout: []byte(installedRunHelp)}, nil
		}
		runArgs = cmd.Args
		return adapter.ProcessResult{Stdout: []byte(`{"type":"text","sessionID":"ses_1","part":{"type":"text","text":"done"}}`)}, nil
	}}
	client := NewWithRequiredVersion("opencode", runner, "", "1.18.16")
	probe, err := client.Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if probe.Version != "2.1.4" || probe.Capabilities["pinned_bootstrap_version"] != "1.18.16" || probe.Capabilities["cli_contract"] != "verified 2.1.4" {
		t.Fatalf("observed version not recorded honestly: %#v", probe)
	}
	result, err := client.Run(context.Background(), adapter.Request{TaskID: "T", Title: "t", Worktree: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if len(runArgs) < 2 || runArgs[0] != "run" || runArgs[1] != "--standalone" || result.SessionID != "ses_1" || result.FinalText != "done" {
		t.Fatalf("2.x run argv/stream: %v %#v", runArgs, result)
	}
}

func TestOpenCodePinnedProbeRefusesDifferentVersionWithoutContract(t *testing.T) {
	runner := &mockRunner{runFunc: func(_ context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
		if cmd.Args[0] == "--version" {
			return adapter.ProcessResult{Stdout: []byte("opencode v2.1.4\n")}, nil
		}
		return adapter.ProcessResult{Stdout: []byte(strings.ReplaceAll(installedRunHelp, "--standalone", "--other"))}, nil
	}}
	_, err := NewWithRequiredVersion("opencode", runner, "", "1.18.16").Probe(context.Background())
	if err == nil || !strings.Contains(err.Error(), "opencode v2.1.4") || !strings.Contains(err.Error(), "1.18.16") || !strings.Contains(err.Error(), "--standalone") {
		t.Fatalf("mismatch not reported honestly: %v", err)
	}
}
