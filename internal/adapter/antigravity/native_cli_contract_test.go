package antigravity_test

import (
	"context"
	"slices"
	"testing"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/adapter/antigravity"
)

func TestAgyUsesInstalledNativePrintContract(t *testing.T) {
	var command adapter.Command
	client := antigravity.New("agy", &mockRunner{runFn: func(_ context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
		command = cmd
		return adapter.ProcessResult{Stdout: []byte(`{"conversation_id":"native-session","status":"SUCCESS","response":"proof","model":"observed-agy-model","num_turns":1,"usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}}`)}, nil
	}})
	result, err := client.Run(context.Background(), adapter.Request{TaskID: "agy-proof", Title: "Return proof", Worktree: t.TempDir(), Model: "gemini-3.8-flash-high"})
	if err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"--print", "--output-format", "--mode"} {
		if !slices.Contains(command.Args, flag) {
			t.Fatalf("native CLI flag %s missing: %v", flag, command.Args)
		}
	}
	for _, retired := range []string{"exec", "--json", "--cd", "--dangerously-skip-permissions"} {
		if slices.Contains(command.Args, retired) {
			t.Fatalf("unsupported or bypass argument %s: %v", retired, command.Args)
		}
	}
	if result.Model != "observed-agy-model" || result.SessionID != "native-session" || result.FinalText != "proof" || result.Status != adapter.StatusSuccess || result.Usage.PromptTokens == nil || *result.Usage.PromptTokens != 10 {
		t.Fatalf("native final envelope was not preserved: %#v", result)
	}
}

func TestAgyRefusesFailedOrMissingNativeFinalResult(t *testing.T) {
	for _, output := range []string{`{"conversation_id":"s","status":"ERROR","response":"quota error"}`, `{"type":"status","state":"working"}`, "not json"} {
		client := antigravity.New("agy", &mockRunner{runFn: func(context.Context, adapter.Command) (adapter.ProcessResult, error) {
			return adapter.ProcessResult{Stdout: []byte(output)}, nil
		}})
		result, err := client.Run(context.Background(), adapter.Request{TaskID: "agy-failure", Title: "Return proof", Worktree: t.TempDir()})
		if err == nil && result.Status == adapter.StatusSuccess {
			t.Fatalf("zero exit accepted without a successful native final result: %s", output)
		}
	}
}

func TestAgyFinalResultCannotOverrideProcessFailure(t *testing.T) {
	for _, resume := range []bool{false, true} {
		for _, failure := range []struct {
			name    string
			process adapter.ProcessResult
			want    adapter.Status
		}{
			{"exit", adapter.ProcessResult{ExitCode: 7}, adapter.StatusFailure},
			{"timeout", adapter.ProcessResult{TimedOut: true}, adapter.StatusFailure},
			{"cancelled", adapter.ProcessResult{Cancelled: true}, adapter.StatusFailure},
			{"truncated", adapter.ProcessResult{OutputTruncated: true}, adapter.StatusBlocked},
		} {
			t.Run(failure.name, func(t *testing.T) {
				client := antigravity.New("agy", &mockRunner{runFn: func(context.Context, adapter.Command) (adapter.ProcessResult, error) {
					process := failure.process
					process.Stdout = []byte(`{"status":"SUCCESS","conversation_id":"session","response":"claimed success"}`)
					return process, nil
				}})
				request := adapter.Request{TaskID: "failure", Title: "proof", Worktree: t.TempDir()}
				var result adapter.Result
				var err error
				if resume {
					result, err = client.Resume(context.Background(), "session", request)
				} else {
					result, err = client.Run(context.Background(), request)
				}
				if err != nil || result.Status != failure.want {
					t.Fatalf("resume=%t status=%s want=%s err=%v", resume, result.Status, failure.want, err)
				}
			})
		}
	}
}

func TestAgyProbeRequiresNativeVersionAndFlags(t *testing.T) {
	for _, state := range []string{"missing-version", "version-error", "help-error", "missing-flags"} {
		t.Run(state, func(t *testing.T) {
			client := antigravity.New("agy", &mockRunner{runFn: func(_ context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
				if cmd.Args[0] == "--version" {
					if state == "missing-version" {
						return adapter.ProcessResult{}, nil
					}
					if state == "version-error" {
						return adapter.ProcessResult{ExitCode: 1}, nil
					}
					return adapter.ProcessResult{Stdout: []byte("1.2.14")}, nil
				}
				if state == "help-error" {
					return adapter.ProcessResult{ExitCode: 1}, nil
				}
				return adapter.ProcessResult{Stdout: []byte("--print")}, nil
			}})
			if _, err := client.Probe(context.Background()); err == nil {
				t.Fatal("unsupported native CLI admitted")
			}
		})
	}
	if _, err := antigravity.New("agy", nil).Probe(context.Background()); err == nil {
		t.Fatal("missing process runner admitted")
	}
}
