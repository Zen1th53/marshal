package claude

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/model"
)

const probeHelp = "--output-format --permission-mode --permission-prompts --strict-mcp-config --add-dir --resume"

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
			// Probe requires every flag the governed stream lifecycle uses, so
			// the fixture lists the same set the real CLI reports.
			return adapter.ProcessResult{Stdout: []byte(
				"--print --output-format --input-format --permission-mode " +
					"--permission-prompts --strict-mcp-config --add-dir --resume",
			), ExitCode: 0}, nil
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

func TestClaudeProbeReservesIndependentHelpBudget(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	var versionDeadline, helpDeadline time.Time
	runner := &mockRunner{runFunc: func(runCtx context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
		deadline, ok := runCtx.Deadline()
		if !ok {
			t.Fatal("metadata phase has no deadline")
		}
		switch cmd.Args[0] {
		case "--version":
			versionDeadline = deadline
			time.Sleep(120 * time.Millisecond)
			return adapter.ProcessResult{Stdout: []byte("1.0.0"), ExitCode: 0}, nil
		case "--help":
			helpDeadline = deadline
			return adapter.ProcessResult{Stdout: []byte(probeHelp), ExitCode: 0}, nil
		default:
			t.Fatalf("unexpected args: %q", cmd.Args)
			return adapter.ProcessResult{}, nil
		}
	}}
	if _, err := New("claude", runner).Probe(ctx); err != nil {
		t.Fatalf("Probe failed: %v", err)
	}
	if !helpDeadline.After(versionDeadline) || time.Until(helpDeadline) < 100*time.Millisecond {
		t.Fatalf("phase deadlines: version=%v help=%v", versionDeadline, helpDeadline)
	}
}

func TestClaudeProbePreservesCancellationAndRunnerErrors(t *testing.T) {
	brokerErr := errors.New("broker unavailable")
	tests := []struct {
		name  string
		ctx   func() context.Context
		run   func(context.Context, adapter.Command) (adapter.ProcessResult, error)
		want  error
		calls int
	}{
		{"already cancelled", func() context.Context { ctx, cancel := context.WithCancel(context.Background()); cancel(); return ctx }, nil, context.Canceled, 0},
		{"runner deadline", context.Background, func(context.Context, adapter.Command) (adapter.ProcessResult, error) {
			return adapter.ProcessResult{}, context.DeadlineExceeded
		}, context.DeadlineExceeded, 1},
		{"broker failure", context.Background, func(context.Context, adapter.Command) (adapter.ProcessResult, error) {
			return adapter.ProcessResult{}, brokerErr
		}, brokerErr, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			runner := &mockRunner{runFunc: func(ctx context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
				calls++
				return tt.run(ctx, cmd)
			}}
			_, err := New("claude", runner).Probe(tt.ctx())
			if !errors.Is(err, tt.want) || !errors.Is(err, model.ErrUnavailable) || calls != tt.calls {
				t.Fatalf("error=%v calls=%d, want %v and %d calls", err, calls, tt.want, tt.calls)
			}
		})
	}
}

func TestClaudeProbePreservesParentDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	runner := &mockRunner{runFunc: func(runCtx context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
		if cmd.Args[0] == "--version" {
			return adapter.ProcessResult{Stdout: []byte("1.0.0")}, nil
		}
		<-runCtx.Done()
		return adapter.ProcessResult{}, runCtx.Err()
	}}
	_, err := New("claude", runner).Probe(ctx)
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, model.ErrUnavailable) {
		t.Fatalf("Probe error=%v, want deadline and unavailable", err)
	}
}

func TestClaudeProbeRequiresGovernedFlags(t *testing.T) {
	runner := &mockRunner{runFunc: func(_ context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
		if cmd.Args[0] == "--version" {
			return adapter.ProcessResult{Stdout: []byte("1.0.0")}, nil
		}
		return adapter.ProcessResult{Stdout: []byte("--output-format --permission-mode --permission-prompts --strict-mcp-config --add-dir")}, nil
	}}
	if _, err := New("claude", runner).Probe(context.Background()); !errors.Is(err, model.ErrUnavailable) || !strings.Contains(err.Error(), "--resume") {
		t.Fatalf("Probe error=%v, want missing --resume", err)
	}
}

func TestClaudeProbeRejectsIncompleteMetadata(t *testing.T) {
	cases := []struct {
		name   string
		result adapter.ProcessResult
	}{
		{"nonzero exit", adapter.ProcessResult{ExitCode: 1}},
		{"timeout", adapter.ProcessResult{TimedOut: true}},
		{"cancelled", adapter.ProcessResult{Cancelled: true}},
		{"truncated", adapter.ProcessResult{OutputTruncated: true}},
	}
	for _, phase := range []string{"--version", "--help"} {
		for _, tc := range cases {
			t.Run(phase+"/"+tc.name, func(t *testing.T) {
				calls := 0
				runner := &mockRunner{runFunc: func(_ context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
					calls++
					if cmd.Args[0] == phase {
						return tc.result, nil
					}
					return adapter.ProcessResult{Stdout: []byte("1.0.0\n"), ExitCode: 0}, nil
				}}
				if _, err := New("claude", runner).Probe(context.Background()); !errors.Is(err, model.ErrUnavailable) {
					t.Fatalf("Probe error=%v, want unavailable", err)
				}
				wantCalls := 2
				if phase == "--version" {
					wantCalls = 1
				}
				if calls != wantCalls {
					t.Fatalf("calls=%d, want %d", calls, wantCalls)
				}
			})
		}
	}
}

func TestGovernedClaudeRunPropagatesModel(t *testing.T) {
	const model = "claude-sonnet-4-5"
	worktree := "/tmp/worktree"
	var captured adapter.Command
	runner := &mockRunner{
		runFunc: func(ctx context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
			captured = cmd
			return adapter.ProcessResult{
				ExitCode:  0,
				StartedAt: time.Now().Add(-time.Second),
				EndedAt:   time.Now(),
			}, nil
		},
	}
	client := New("claude", runner)
	_, err := client.Run(context.Background(), adapter.Request{
		TaskID:   "TASK-MODEL",
		Title:    "Test Claude Model Selection",
		Worktree: worktree,
		Model:    model,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	value, ok := argValue(captured.Args, "--model")
	if !ok || value != model {
		t.Fatalf("expected --model %q as a single argv value, got %q (present=%v) in %q", model, value, ok, captured.Args)
	}
	modelFlags := 0
	for _, arg := range captured.Args {
		if arg == "--model" {
			modelFlags++
		}
	}
	if modelFlags != 1 {
		t.Fatalf("expected exactly one --model selector, got %d in %q", modelFlags, captured.Args)
	}
	if !hasArg(captured.Args, "-p") {
		t.Fatalf("governed print mode must remain in %q", captured.Args)
	}
	if value, ok := argValue(captured.Args, "--permission-mode"); !ok || value != "bypassPermissions" {
		t.Fatalf("governed permission mode must remain intact, got %q (present=%v)", value, ok)
	}
	if value, ok := argValue(captured.Args, "--permission-prompts"); !ok || value != "none" {
		t.Fatalf("governed permission prompts must remain fail-closed, got %q (present=%v)", value, ok)
	}
	if captured.Dir != worktree {
		t.Fatalf("governed worktree directory = %q, want %q", captured.Dir, worktree)
	}
}

func TestGovernedClaudeRunSuccess(t *testing.T) {
	runner := &mockRunner{
		runFunc: func(ctx context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
			want := []string{"--output-format", "json", "--permission-mode", "bypassPermissions", "--permission-prompts", "none"}
			if len(cmd.Args) != 8 || cmd.Args[0] != "-p" || !slices.Equal(cmd.Args[2:], want) {
				t.Fatalf("governed argv = %q", cmd.Args)
			}
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

func TestGovernedClaudeRunUsesAssignedModel(t *testing.T) {
	for _, selector := range []string{"sonnet", "claude-sonnet-4-6"} {
		t.Run(selector, func(t *testing.T) {
			var actual adapter.Command
			client := New("claude", &mockRunner{runFunc: func(_ context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
				actual = cmd
				return adapter.ProcessResult{ExitCode: 0, Stdout: []byte(`{"result":"marker prepared"}`)}, nil
			}})
			_, err := client.Run(t.Context(), adapter.Request{TaskID: "TASK-model", Title: "exact marker", Worktree: "/tmp/worktree", Model: selector})
			if err != nil {
				t.Fatal(err)
			}
			index := slices.Index(actual.Args, "--model")
			if index < 0 || index+1 >= len(actual.Args) || actual.Args[index+1] != selector {
				t.Fatalf("assigned model %q absent from actual CLI argv: %q", selector, actual.Args)
			}
		})
	}
}

func TestGovernedClaudeRunRefusesInvalidModelBeforeLaunch(t *testing.T) {
	for _, selector := range []string{"--dangerously-skip-permissions", "sonnet\n", "sonnet extra", " ", "bypassPermissions"} {
		t.Run(selector, func(t *testing.T) {
			launched := false
			client := New("claude", &mockRunner{runFunc: func(_ context.Context, _ adapter.Command) (adapter.ProcessResult, error) {
				launched = true
				return adapter.ProcessResult{}, nil
			}})
			_, err := client.Run(t.Context(), adapter.Request{TaskID: "TASK-model", Title: "exact marker", Worktree: "/tmp/worktree", Model: selector})
			if err == nil || launched {
				t.Fatalf("invalid model launched=%v err=%v", launched, err)
			}
		})
	}
}
