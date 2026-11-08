package codex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/model"
)

type capturingRunner struct {
	command         adapter.Command
	result          adapter.ProcessResult
	probeCompatible bool
}

func (r *capturingRunner) Run(_ context.Context, command adapter.Command) (adapter.ProcessResult, error) {
	r.command = command
	if len(command.Args) == 1 && command.Args[0] == "--version" {
		return adapter.ProcessResult{Stdout: []byte("codex-cli 0.test.0\n")}, nil
	}
	if len(command.Args) == 2 && command.Args[0] == "exec" && command.Args[1] == "--help" {
		help := "--json --sandbox --ephemeral --ignore-user-config --cd"
		if !r.probeCompatible {
			help = "--json"
		}
		return adapter.ProcessResult{Stdout: []byte(help)}, nil
	}
	if len(command.Args) == 2 && command.Args[0] == "debug" && command.Args[1] == "models" {
		return adapter.ProcessResult{Stdout: []byte(`{"models":[{"slug":"gpt-5-codex","display_name":"GPT 5 Codex","visibility":"visible"},{"slug":"gpt-5.6-terra","display_name":"GPT 5.6 Terra","visibility":"visible"}]}`)}, nil
	}
	if len(command.Args) == 2 && command.Args[0] == "doctor" && command.Args[1] == "--json" {
		return adapter.ProcessResult{Stdout: []byte(`{"checks":{"config.load":{"details":{"model":"gpt-5.6-terra"}}}}`)}, nil
	}
	return r.result, nil
}

func TestProbeRequiresCurrentNativeFlags(t *testing.T) {
	binary := fakeProbeBinary(t, true)
	client := New(binary, &capturingRunner{probeCompatible: true})
	probe, err := client.Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !probe.Available || probe.Version != "codex-cli 0.test.0" {
		t.Fatalf("probe = %#v", probe)
	}
}

func TestProbeRejectsCLIWithoutRequiredFlags(t *testing.T) {
	binary := fakeProbeBinary(t, false)
	client := New(binary, &capturingRunner{probeCompatible: false})
	if _, err := client.Probe(context.Background()); err == nil {
		t.Fatal("probe accepted incompatible CLI help")
	}
}

func TestGovernedRunUsesExternalSandboxAndNormalizesJSONL(t *testing.T) {
	runner := &capturingRunner{result: adapter.ProcessResult{
		Stdout: []byte("{\"type\":\"thread.started\",\"thread_id\":\"thread-123\"}\n" +
			"{\"type\":\"item.completed\",\"item\":{\"type\":\"agent_message\",\"text\":\"done\"}}\n"),
		ExitCode:  0,
		StartedAt: time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC),
		EndedAt:   time.Date(2026, 8, 11, 12, 0, 1, 0, time.UTC),
	}}
	client := New("/usr/bin/codex", runner)
	result, err := client.Run(context.Background(), adapter.Request{
		TaskID: "TASK-001", Title: "change one file", Worktree: "/repo/task",
		BaseCommit: "abc", HeadCommit: "abc",
		AllowedOperations: []string{"filesystem.write", "shell.execute"},
		EvidenceRequired:  []string{"go test ./..."},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"exec", "--json", "-C", "/repo/task", "--ephemeral", "--ignore-user-config", "--dangerously-bypass-approvals-and-sandbox", "-"}
	if !slices.Equal(runner.command.Args, want) {
		t.Fatalf("args = %#v, want %#v", runner.command.Args, want)
	}
	joined := strings.Join(runner.command.Args, " ")
	for _, forbidden := range []string{"--sandbox", "workspace-write", "danger-full-access", "--search", "--dangerously-bypass-hook-trust"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("forbidden flag %q in %s", forbidden, joined)
		}
	}
	if runner.command.Dir != "/repo/task" || !strings.Contains(string(runner.command.Stdin), "TASK-001") {
		t.Fatalf("command = %#v", runner.command)
	}
	if result.Status != adapter.StatusSuccess || result.SessionID != "thread-123" ||
		result.FinalText != "done" || result.ExitCode != 0 {
		t.Fatalf("result = %#v", result)
	}
}

func TestRunPassesTypedModelToNativeCLI(t *testing.T) {
	runner := &capturingRunner{result: adapter.ProcessResult{ExitCode: 0}}
	_, err := New("/usr/bin/codex", runner).Run(context.Background(), adapter.Request{
		TaskID: "TASK-001", Title: "choose model", Worktree: "/repo/task", Model: "gpt-5-codex",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"exec", "--json", "-C", "/repo/task", "--ephemeral", "--ignore-user-config", "--model", "gpt-5-codex", "--dangerously-bypass-approvals-and-sandbox", "-"}
	if !slices.Equal(runner.command.Args, want) {
		t.Fatalf("args = %#v, want %#v", runner.command.Args, want)
	}
}

func TestRunNormalizesNonzeroExitAsFailure(t *testing.T) {
	runner := &capturingRunner{result: adapter.ProcessResult{ExitCode: 7, Stderr: []byte("failed")}}
	result, err := New("/usr/bin/codex", runner).Run(context.Background(), adapter.Request{
		TaskID: "TASK-001", Title: "fail", Worktree: "/repo/task",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != adapter.StatusFailure || result.ExitCode != 7 {
		t.Fatalf("result = %#v", result)
	}
}

func TestRunForwardsHeartbeatToWorker(t *testing.T) {
	runner := &capturingRunner{result: adapter.ProcessResult{ExitCode: 0}}
	heartbeat := func() {}
	_, err := New("/usr/bin/codex", runner).Run(context.Background(), adapter.Request{
		TaskID: "TASK-001", Title: "heartbeat", Worktree: "/repo/task",
		Heartbeat: heartbeat, HeartbeatInterval: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if runner.command.Heartbeat == nil || runner.command.HeartbeatInterval != 5*time.Second {
		t.Fatalf("heartbeat command = %#v", runner.command)
	}
}

func TestRunReportsTruncatedEvidenceAsBlocked(t *testing.T) {
	runner := &capturingRunner{result: adapter.ProcessResult{
		ExitCode: 0, OutputTruncated: true,
	}}
	result, err := New("/usr/bin/codex", runner).Run(context.Background(), adapter.Request{
		TaskID: "TASK-001", Title: "truncated", Worktree: "/repo/task",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != adapter.StatusBlocked {
		t.Fatalf("status = %s, want blocked", result.Status)
	}
}

func fakeProbeBinary(t *testing.T, compatible bool) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "codex")
	help := "--json --sandbox --ephemeral --ignore-user-config --cd"
	if !compatible {
		help = "--json"
	}
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"--version\" ]; then echo 'codex-cli 0.test.0'; exit 0; fi\n" +
		"if [ \"$1\" = \"exec\" ] && [ \"$2\" = \"--help\" ]; then echo '" + help + "'; exit 0; fi\n" +
		"exit 2\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestGovernedRunStillRejectsCallerSuppliedBypass(t *testing.T) {
	runner := &capturingRunner{}
	_, err := New("codex", runner).Run(context.Background(), adapter.Request{
		TaskID: "TASK-001", Title: "--dangerously-bypass-approvals-and-sandbox", Worktree: "/repo/task",
	})
	if err == nil || runner.command.Path != "" {
		t.Fatalf("caller bypass reached runner: err=%v command=%+v", err, runner.command)
	}
}

// Controlled runners exercise the Probe boundary without starting host processes.
type probeRunnerFunc func(context.Context, adapter.Command) (adapter.ProcessResult, error)

func (f probeRunnerFunc) Run(ctx context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
	return f(ctx, cmd)
}

func TestProbeIndependentMetadataBudgets(t *testing.T) {
	calls := 0
	runner := probeRunnerFunc(func(ctx context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
		calls++
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) < 70*time.Millisecond {
			t.Fatalf("command %v did not receive its own finite budget", cmd.Args)
		}
		// Combined startup exceeds one budget, but each command fits its own.
		timer := time.NewTimer(60 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return adapter.ProcessResult{}, ctx.Err()
		case <-timer.C:
			return (&capturingRunner{probeCompatible: true}).Run(ctx, cmd)
		}
	})
	client := NewWithModels("codex", runner, []ModelInfo{{Slug: "test"}}, "test")
	if _, err := client.probe(context.Background(), 100*time.Millisecond, 10*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("required calls = %d", calls)
	}
}

func TestProbeMetadataFailuresPreserveCauses(t *testing.T) {
	for _, tc := range []struct {
		name     string
		failCall int
		parent   string
		want     error
	}{
		{"version timeout", 1, "", context.DeadlineExceeded},
		{"help timeout", 2, "", context.DeadlineExceeded},
		{"pre-canceled", 0, "canceled", context.Canceled},
		{"parent deadline", 1, "deadline", context.DeadlineExceeded},
		{"parent cancellation", 2, "during", context.Canceled},
		{"real denial", 1, "denial", model.ErrPolicyDenied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.parent == "canceled" {
				cancel()
			}
			if tc.parent == "deadline" {
				var deadlineCancel context.CancelFunc
				ctx, deadlineCancel = context.WithTimeout(ctx, 10*time.Millisecond)
				defer deadlineCancel()
			}
			calls := 0
			runner := probeRunnerFunc(func(commandCtx context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
				calls++
				if calls == tc.failCall {
					if tc.parent != "denial" {
						if tc.parent == "during" {
							cancel()
						}
						<-commandCtx.Done()
					}
					// CapabilityRunner can mask the underlying context error.
					return adapter.ProcessResult{}, model.ErrPolicyDenied
				}
				return (&capturingRunner{probeCompatible: true}).Run(commandCtx, cmd)
			})
			_, err := New("codex", runner).probe(ctx, 30*time.Millisecond, 10*time.Millisecond)
			if !errors.Is(err, model.ErrUnavailable) || !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want unavailable and %v", err, tc.want)
			}
			if calls != tc.failCall {
				t.Fatalf("calls = %d, want %d", calls, tc.failCall)
			}
			if calls > 0 && !errors.Is(err, model.ErrPolicyDenied) {
				t.Fatalf("lost runner denial: %v", err)
			}
			if tc.parent == "denial" && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
				t.Fatalf("invented context cause: %v", err)
			}
		})
	}
}

func TestProbeCatalogHasSeparateBoundedBudget(t *testing.T) {
	calls := 0
	runner := probeRunnerFunc(func(ctx context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
		calls++
		if calls <= 2 {
			return (&capturingRunner{probeCompatible: true}).Run(ctx, cmd)
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 20*time.Millisecond {
			t.Fatal("catalog has no bounded budget")
		}
		<-ctx.Done()
		return adapter.ProcessResult{}, ctx.Err()
	})
	probe, err := New("codex", runner).probe(context.Background(), 100*time.Millisecond, 10*time.Millisecond)
	if err != nil || !probe.Available {
		t.Fatalf("optional discovery changed required success: %v", err)
	}
}

func TestProbeNonzeroExitPreservesUnavailable(t *testing.T) {
	runner := probeRunnerFunc(func(context.Context, adapter.Command) (adapter.ProcessResult, error) {
		return adapter.ProcessResult{ExitCode: 7}, nil
	})
	_, err := New("codex", runner).Probe(context.Background())
	if !errors.Is(err, model.ErrUnavailable) || !strings.Contains(err.Error(), "exit code 7") {
		t.Fatalf("error = %v", err)
	}
}

func TestProbeProductionBudgets(t *testing.T) {
	calls := 0
	runner := probeRunnerFunc(func(ctx context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
		calls++
		deadline, ok := ctx.Deadline()
		remaining := time.Until(deadline)
		if !ok || remaining <= 5*time.Second || remaining > 20*time.Second {
			t.Fatalf("required startup budget = %v, deadline present = %v", remaining, ok)
		}
		return (&capturingRunner{probeCompatible: true}).Run(ctx, cmd)
	})
	_, err := NewWithModels("codex", runner, []ModelInfo{{Slug: "test"}}, "test").Probe(context.Background())
	if err != nil || calls != 2 {
		t.Fatalf("calls = %d, error = %v", calls, err)
	}
}

func TestProbeRejectsEachMissingNativeFlag(t *testing.T) {
	flags := []string{"--json", "--sandbox", "--ephemeral", "--ignore-user-config", "--cd"}
	for _, missing := range flags {
		t.Run(missing, func(t *testing.T) {
			calls := 0
			runner := probeRunnerFunc(func(ctx context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
				calls++
				if calls == 1 {
					return (&capturingRunner{}).Run(ctx, cmd)
				}
				return adapter.ProcessResult{Stdout: []byte(strings.ReplaceAll(strings.Join(flags, " "), missing, ""))}, nil
			})
			_, err := New("codex", runner).Probe(context.Background())
			if !errors.Is(err, model.ErrUnavailable) || !strings.Contains(err.Error(), missing) || calls != 2 {
				t.Fatalf("calls = %d, error = %v", calls, err)
			}
		})
	}
}
