package antigravity_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/adapter/antigravity"
	"github.com/Zen1th53/marshal/internal/model"
)

func nativeRequest() adapter.Request {
	return adapter.Request{TaskID: "native-read", Title: "Read-only reply", Worktree: "/isolated/cwd", TrustedContext: "Reply without tools."}
}

func TestCurrentAntigravityDialectCwdAndPermissions(t *testing.T) {
	var command adapter.Command
	runner := &mockRunner{runFn: func(_ context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
		command = cmd
		return adapter.ProcessResult{Stdout: []byte(`{"status":"SUCCESS","response":"ready","conversation_id":"fresh"}`)}, nil
	}}
	client := antigravity.New("agy", runner)
	req := nativeRequest()
	req.Effort = "high"
	req.AllowedOperations = []string{"respond_only"}
	if _, err := client.Run(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	want := []string{"--mode", "plan", "--sandbox", "--disable-slash-commands", "--output-format", "json", "--model", antigravity.DefaultModel, "--effort", "high", "--new-project", "--print"}
	if !slices.Equal(command.Args[:len(command.Args)-1], want) || command.Dir != req.Worktree || len(command.Env) != 0 {
		t.Fatal("native dialect, permissions, process cwd or inherited runner contract changed")
	}
	for _, forbidden := range []string{"exec", "--json", "--cd", "--resume", "--dangerously-skip-permissions"} {
		if slices.Contains(command.Args, forbidden) {
			t.Fatal("unsupported or permission-bypass argument emitted")
		}
	}
	var prompt map[string]any
	if err := json.Unmarshal([]byte(command.Args[len(command.Args)-1]), &prompt); err != nil || prompt["task_id"] != req.TaskID || prompt["trusted_context"] != req.TrustedContext {
		t.Fatal("task context was not preserved as JSON data")
	}
	if _, err := client.Resume(t.Context(), "fresh", req); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(command.Args, "--new-project") || !slices.Contains(command.Args, "--conversation") {
		t.Fatal("resume must bind the explicit conversation")
	}
	called := false
	runner.runFn = func(context.Context, adapter.Command) (adapter.ProcessResult, error) {
		called = true
		return adapter.ProcessResult{}, nil
	}
	req.Effort = "invalid value"
	if _, err := client.Run(t.Context(), req); !errors.Is(err, model.ErrInvalid) || called {
		t.Fatal("invalid effort must refuse before executing")
	}
}

func TestCurrentAntigravityOutputAndFailureSemantics(t *testing.T) {
	for _, tc := range []struct {
		name          string
		process       adapter.ProcessResult
		status        adapter.Status
		text, session string
		usage         bool
	}{
		{"success", adapter.ProcessResult{Stdout: []byte(`{"conversation_id":"new","status":"SUCCESS","response":"final only","reasoning":"INTERNAL_SENTINEL","thinking":"INTERNAL_SENTINEL","steps":[{"text":"INTERNAL_SENTINEL"}],"usage":{"input_tokens":7,"output_tokens":3}}`)}, adapter.StatusSuccess, "final only", "new", true},
		{"unknown_usage", adapter.ProcessResult{Stdout: []byte(`{"status":"SUCCESS","response":"reply"}`)}, adapter.StatusSuccess, "reply", "", false},
		{"denied", adapter.ProcessResult{Stdout: []byte(`{"status":"SUCCESS","response":"","denied_actions":[{"action":"command"}]}`)}, adapter.StatusBlocked, "", "", false},
		{"denied_with_text", adapter.ProcessResult{Stdout: []byte(`{"status":"SUCCESS","response":"permission refused","denied_actions":[{}]}`)}, adapter.StatusBlocked, "permission refused", "", false},
		{"empty", adapter.ProcessResult{Stdout: []byte(`{"status":"SUCCESS","response":" "}`)}, adapter.StatusBlocked, " ", "", false},
		{"error_envelope", adapter.ProcessResult{Stdout: []byte(`{"status":"ERROR","response":"provider error"}`)}, adapter.StatusFailure, "provider error", "", false},
		{"is_error", adapter.ProcessResult{Stdout: []byte(`{"status":"SUCCESS","is_error":true,"response":"provider error"}`)}, adapter.StatusFailure, "provider error", "", false},
		{"missing_status", adapter.ProcessResult{Stdout: []byte(`{"response":"unattested"}`)}, adapter.StatusFailure, "unattested", "", false},
		{"invalid_json", adapter.ProcessResult{Stdout: []byte(`not-json`)}, adapter.StatusFailure, "", "", false},
		{"exit", adapter.ProcessResult{ExitCode: 2, Stdout: []byte(`{"status":"SUCCESS","response":"partial"}`)}, adapter.StatusFailure, "partial", "", false},
		{"timeout", adapter.ProcessResult{TimedOut: true, Stdout: []byte(`{"status":"SUCCESS","response":"partial"}`)}, adapter.StatusFailure, "partial", "", false},
		{"cancelled", adapter.ProcessResult{Cancelled: true, Stdout: []byte(`{"status":"SUCCESS","response":"partial"}`)}, adapter.StatusFailure, "partial", "", false},
		{"truncated", adapter.ProcessResult{OutputTruncated: true, Stdout: []byte(`{"status":"SUCCESS","response":"partial"}`)}, adapter.StatusBlocked, "partial", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := antigravity.New("agy", &mockRunner{runFn: func(context.Context, adapter.Command) (adapter.ProcessResult, error) { return tc.process, nil }})
			for _, resume := range []bool{false, true} {
				var got adapter.Result
				var err error
				if resume {
					got, err = client.Resume(t.Context(), "session", nativeRequest())
				} else {
					got, err = client.Run(t.Context(), nativeRequest())
				}
				if err != nil || got.Status != tc.status || got.FinalText != tc.text || got.SessionID != tc.session || got.Usage.Reported != tc.usage {
					t.Fatal("final result classification or normalization mismatch")
				}
				if strings.Contains(got.FinalText, "INTERNAL_SENTINEL") || len(got.Events) != 0 {
					t.Fatal("internal reasoning entered normalized final evidence")
				}
				if tc.usage && (got.Usage.PromptTokens == nil || *got.Usage.PromptTokens != 7 || got.Usage.CompletionTokens == nil || *got.Usage.CompletionTokens != 3 || got.Usage.TotalTokens != nil || got.Usage.CostUSD != nil) {
					t.Fatal("reported usage must preserve unknown total/cost")
				}
			}
		})
	}
	client := antigravity.New("agy", &mockRunner{runFn: func(context.Context, adapter.Command) (adapter.ProcessResult, error) {
		return adapter.ProcessResult{}, context.Canceled
	}})
	if _, err := client.Run(t.Context(), nativeRequest()); !errors.Is(err, context.Canceled) {
		t.Fatal("runner cancellation error lost")
	}
}

func TestCurrentAntigravityProbeRejectsOldDialectAndUnknownVersion(t *testing.T) {
	for _, version := range []string{"1.2.14", ""} {
		client := antigravity.New("agy", &mockRunner{runFn: func(_ context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
			if cmd.Args[0] == "--version" {
				return adapter.ProcessResult{Stdout: []byte(version)}, nil
			}
			return adapter.ProcessResult{Stdout: []byte("exec --json --cd --resume")}, nil
		}})
		if _, err := client.Probe(t.Context()); !errors.Is(err, model.ErrUnavailable) {
			t.Fatal("unsupported CLI or unreported version accepted")
		}
	}
}

// This opt-in smoke executes a NEW real native conversation through Client.Run.
// It never resumes a user session or changes provider settings/configuration.
func TestCurrentAntigravityNativeSmoke(t *testing.T) {
	if os.Getenv("MARSHAL_AGY_NATIVE_SMOKE") != "1" {
		t.Skip("opt-in real provider test")
	}
	dir := os.Getenv("MARSHAL_AGY_SMOKE_ARTIFACT_DIR")
	if !filepath.IsAbs(dir) {
		t.Fatal("absolute persistent smoke artifact directory required")
	}
	if err := os.MkdirAll(filepath.Join(dir, "cwd"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Second)
	defer cancel()
	var invocations []adapter.Command
	var providerPID int
	runner := &mockRunner{runFn: func(ctx context.Context, command adapter.Command) (adapter.ProcessResult, error) {
		invocations = append(invocations, command)
		cmd := exec.CommandContext(ctx, command.Path, command.Args...)
		cmd.Dir = command.Dir
		var stdout, stderr strings.Builder
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		started := time.Now().UTC()
		err := cmd.Run()
		ended := time.Now().UTC()
		result := adapter.ProcessResult{Stdout: []byte(stdout.String()), Stderr: []byte(stderr.String()), StartedAt: started, EndedAt: ended}
		if cmd.ProcessState != nil {
			result.ExitCode = cmd.ProcessState.ExitCode()
		}
		if cmd.Process != nil {
			providerPID = cmd.Process.Pid
		}
		result.TimedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
		result.Cancelled = errors.Is(ctx.Err(), context.Canceled)
		var exitError *exec.ExitError
		if err != nil && !errors.As(err, &exitError) {
			return result, err
		}
		return result, nil
	}}
	client := antigravity.New("/sbin/agy", runner)
	probe, err := client.Probe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	req := nativeRequest()
	req.Worktree = filepath.Join(dir, "cwd")
	req.AllowedOperations = []string{"respond_only"}
	req.Title = "Reply MARSHAL_AGY_ADAPTER_OK without tools"
	req.TrustedContext = "Read-only smoke test. Do not use tools, commands, MCP, files or writes. Return exactly MARSHAL_AGY_ADAPTER_OK as your final response."
	got, err := client.Run(ctx, req)
	metadata := map[string]any{"start_utc": got.StartedAt, "end_utc": got.EndedAt, "pid": providerPID, "version": probe.Version, "model_requested": antigravity.DefaultModel, "session_id": got.SessionID, "status": got.Status, "exit_code": got.ExitCode, "timed_out": got.TimedOut, "cancelled": got.Cancelled, "final_text": got.FinalText, "usage": got.Usage, "method": "actual Client.Run with real exec process runner; new project; no resume"}
	if source, readErr := os.ReadFile("antigravity.go"); readErr == nil {
		metadata["adapter_source_sha256"] = fmt.Sprintf("%x", sha256.Sum256(source))
	}
	if snapshot, snapshotErr := exec.Command("git", "rev-parse", "HEAD").Output(); snapshotErr == nil {
		metadata["source_snapshot"] = strings.TrimSpace(string(snapshot))
	}
	if len(invocations) == 3 {
		metadata["argv_without_prompt"] = append([]string{invocations[2].Path}, invocations[2].Args[:len(invocations[2].Args)-1]...)
		metadata["cwd"] = invocations[2].Dir
	}
	encoded, _ := json.MarshalIndent(metadata, "", "  ")
	if writeErr := os.WriteFile(filepath.Join(dir, "execution.json"), encoded, 0600); writeErr != nil {
		t.Fatal(writeErr)
	}
	if writeErr := os.WriteFile(filepath.Join(dir, "stdout.json"), got.Stdout, 0600); writeErr != nil {
		t.Fatal(writeErr)
	}
	if writeErr := os.WriteFile(filepath.Join(dir, "stderr.log"), got.Stderr, 0600); writeErr != nil {
		t.Fatal(writeErr)
	}
	if err != nil || got.Status != adapter.StatusSuccess || got.SessionID == "" || !strings.Contains(got.FinalText, "MARSHAL_AGY_ADAPTER_OK") || len(invocations) != 3 {
		t.Fatal("real fresh native adapter reply did not qualify; inspect private artifacts")
	}
	t.Logf("fresh native adapter qualified: version=%s; final reply marker received; private execution metadata saved", probe.Version)
}
