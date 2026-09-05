package antigravity_test

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/adapter/antigravity"
)

type mockRunner struct {
	runFn func(ctx context.Context, cmd adapter.Command) (adapter.ProcessResult, error)
}

func (m *mockRunner) Run(ctx context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
	if m.runFn != nil {
		return m.runFn(ctx, cmd)
	}
	return adapter.ProcessResult{}, nil
}

func TestAntigravityProbe(t *testing.T) {
	ctx := context.Background()
	runner := &mockRunner{
		runFn: func(ctx context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
			if len(cmd.Args) > 0 && cmd.Args[0] == "--version" {
				return adapter.ProcessResult{
					ExitCode: 0,
					Stdout:   []byte("agy version 2.1.0\n"),
				}, nil
			}
			return adapter.ProcessResult{ExitCode: 0}, nil
		},
	}

	client := antigravity.New("agy", runner)
	probe, err := client.Probe(ctx)
	if err != nil {
		t.Fatalf("Probe failed: %v", err)
	}
	if probe.Name != "antigravity" {
		t.Fatalf("expected probe name antigravity, got %s", probe.Name)
	}
	if !probe.Available {
		t.Fatalf("expected available = true")
	}
	if probe.Capabilities["artifacts"] != "native" {
		t.Fatalf("expected native artifacts capability")
	}
}

func TestAntigravityRunAndUsageParsing(t *testing.T) {
	ctx := context.Background()
	var executedCmd adapter.Command

	runner := &mockRunner{
		runFn: func(ctx context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
			executedCmd = cmd
			jsonOutput := `{"type":"status","state":"working"}` + "\n" +
				`{"type":"usage","usage":{"total_tokens":4200,"prompt_tokens":3000,"completion_tokens":1200,"cost_usd":0.012},"session_id":"sess-agy-001"}` + "\n"
			return adapter.ProcessResult{
				ExitCode:  0,
				Stdout:    []byte(jsonOutput),
				StartedAt: time.Now().UTC(),
				EndedAt:   time.Now().UTC(),
			}, nil
		},
	}

	client := antigravity.New("agy", runner)
	req := adapter.Request{
		TaskID:   "task-agy-1",
		Title:    "Implement network protocol handler",
		Worktree: "/tmp/worktree-test",
		Model:    "gemini-3.8-flash",
	}

	result, err := client.Run(ctx, req)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if result.Status != adapter.StatusSuccess {
		t.Fatalf("expected StatusSuccess, got %v", result.Status)
	}
	if result.Adapter != "antigravity" {
		t.Fatalf("expected adapter antigravity, got %s", result.Adapter)
	}

	// Verify command arguments
	foundModel := false
	for i, arg := range executedCmd.Args {
		if arg == "--model" && i+1 < len(executedCmd.Args) && executedCmd.Args[i+1] == "gemini-3.8-flash" {
			foundModel = true
			break
		}
	}
	if !foundModel {
		t.Fatalf("expected --model gemini-3.8-flash in executed command args: %v", executedCmd.Args)
	}

	// Verify usage parsing
	if !result.Usage.Reported {
		t.Fatalf("expected Usage.Reported = true")
	}
	if result.Usage.TotalTokens == nil || *result.Usage.TotalTokens != 4200 {
		t.Fatalf("expected 4200 total tokens, got %v", result.Usage.TotalTokens)
	}
	if result.Usage.CostUSD == nil || *result.Usage.CostUSD != 0.012 {
		t.Fatalf("expected cost 0.012, got %v", result.Usage.CostUSD)
	}
}

func TestAntigravityResume(t *testing.T) {
	ctx := context.Background()
	var executedCmd adapter.Command

	runner := &mockRunner{
		runFn: func(ctx context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
			executedCmd = cmd
			return adapter.ProcessResult{
				ExitCode:  0,
				Stdout:    []byte(`{"type":"resumed","session_id":"sess-resume-100"}` + "\n"),
				StartedAt: time.Now().UTC(),
				EndedAt:   time.Now().UTC(),
			}, nil
		},
	}

	client := antigravity.New("agy", runner)
	req := adapter.Request{
		TaskID:   "task-agy-resume",
		Title:    "Continue network implementation",
		Worktree: "/tmp/worktree-test",
	}

	result, err := client.Resume(ctx, "sess-resume-100", req)
	if err != nil {
		t.Fatalf("Resume failed: %v", err)
	}
	if result.Status != adapter.StatusSuccess {
		t.Fatalf("expected StatusSuccess, got %v", result.Status)
	}

	foundResume := false
	for i, arg := range executedCmd.Args {
		if arg == "--resume" && i+1 < len(executedCmd.Args) && executedCmd.Args[i+1] == "sess-resume-100" {
			foundResume = true
			break
		}
	}
	if !foundResume {
		t.Fatalf("expected --resume sess-resume-100 in executed command args: %v", executedCmd.Args)
	}
}
