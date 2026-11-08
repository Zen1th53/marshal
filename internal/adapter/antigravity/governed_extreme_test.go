//go:build !governed && unix

package antigravity_test

import (
	"context"
	"errors"
	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/adapter/antigravity"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/sessionmcp"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestGovernedAntigravityUsesBoundMCPAndExactModel(t *testing.T) {
	t.Setenv("MARSHAL_MCP_BINARY", "/bin/true")
	root := t.TempDir()
	if err := os.Mkdir(root+"/.agents", 0700); err != nil {
		t.Fatal(err)
	}
	ctx, err := sessionmcp.WithBoundWorker(t.Context(), root, "TASK-agy", func(context.Context, io.Reader, io.Writer) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC()
	end := start.Add(time.Second)
	called := false
	runner := governedRunner(func(ctx context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
		called = true
		if cmd.Args[0] != "--mode" || cmd.Args[1] != "accept-edits" || cmd.Args[7] != "gemini-3.8-flash-high" {
			t.Fatal("governed mode/model missing")
		}
		if strings.Contains(strings.Join(cmd.Args, " "), "dangerously") || len(cmd.Env) != 0 {
			t.Fatal("permission bypass or arbitrary environment")
		}
		req, e := sessionmcp.SandboxRequest(ctx, model.SandboxRequest{Worktree: root})
		if e != nil {
			t.Fatal(e)
		}
		found := false
		for _, b := range req.ReadOnlyBinds {
			if b.Target == "/home/marshal/.gemini/config" {
				data, e := os.ReadFile(b.Source + "/mcp_config.json")
				if e != nil || !strings.Contains(string(data), "marshal") || !strings.Contains(string(data), "relay") {
					t.Fatal("fresh scoped relay missing")
				}
				found = true
			}
		}
		if !found {
			t.Fatal("generated MCP bind missing")
		}
		return adapter.ProcessResult{StartedAt: start, EndedAt: end, Stdout: []byte(`{"status":"SUCCESS","response":"done","conversation_id":"native-session","usage":{"input_tokens":7}}`)}, nil
	})
	req := nativeRequest()
	req.Worktree = root
	req.TaskID = "TASK-agy"
	req.Model = "gemini-3.8-flash-high"
	client := antigravity.NewGoverned("agy", runner)
	if _, e := client.Probe(ctx); e != nil {
		t.Fatal(e)
	}
	result, e := client.Run(ctx, req)
	if e != nil || !called || result.Adapter != "agy" || result.SessionID != "native-session" || result.StartedAt != start || result.EndedAt != end || result.Usage.PromptTokens == nil || *result.Usage.PromptTokens != 7 {
		t.Fatalf("governed result identity lost: %v", e)
	}
}

func TestGovernedAntigravityRefusesUnboundAndMalformedSelectors(t *testing.T) {
	called := false
	runner := &mockRunner{runFn: func(context.Context, adapter.Command) (adapter.ProcessResult, error) {
		called = true
		return adapter.ProcessResult{}, nil
	}}
	client := antigravity.NewGoverned("agy", runner)
	req := nativeRequest()
	req.Model = "gemini-3.8-flash-high"
	if _, err := client.Run(t.Context(), req); err == nil || called {
		t.Fatal("unbound governed launch accepted")
	}
	for _, name := range []string{"", "--dangerously-skip-permissions", "white space", "bad\nmodel", strings.Repeat("a", 129)} {
		req.Model = name
		if _, err := client.Run(t.Context(), req); !errors.Is(err, model.ErrInvalid) || called {
			t.Fatal("malformed model reached launch")
		}
	}
}

func TestGovernedAntigravityProbeHasNoWorkerAuthority(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(root+"/.agents", 0700); err != nil {
		t.Fatal(err)
	}
	var sources []string
	runner := governedRunner(func(ctx context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
		if cmd.Args[1] != "plan" {
			t.Fatal("probe gained write mode")
		}
		req, e := sessionmcp.SandboxRequest(ctx, model.SandboxRequest{Worktree: root})
		if e != nil {
			t.Fatal(e)
		}
		found := false
		for _, b := range req.ReadOnlyBinds {
			if b.Source != b.Target {
				sources = append(sources, b.Source)
			}
			if b.Target == "/home/marshal/.gemini/config" {
				data, e := os.ReadFile(b.Source + "/mcp_config.json")
				if e != nil || strings.Contains(string(data), "relay") || strings.Contains(string(data), "marshal") {
					t.Fatal("probe acquired worker MCP authority")
				}
				found = true
			}
		}
		if !found {
			t.Fatal("probe lacks empty fresh MCP mask")
		}
		return adapter.ProcessResult{Stdout: []byte(`{"status":"SUCCESS","response":"MARSHAL_PROBE_OK"}`)}, nil
	})
	req := nativeRequest()
	req.Worktree = root
	req.Model = "gemini-3.8-flash-high"
	client := antigravity.NewGovernedProbe("agy", runner)
	if _, e := client.Probe(t.Context()); e != nil {
		t.Fatal(e)
	}
	if _, err := client.Run(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	for _, p := range sources {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatal("probe generated config survived")
		}
	}
}

func governedRunner(run func(context.Context, adapter.Command) (adapter.ProcessResult, error)) *mockRunner {
	return &mockRunner{runFn: func(ctx context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
		if len(cmd.Args) == 1 && cmd.Args[0] == "--version" {
			return adapter.ProcessResult{Stdout: []byte("1.2.14")}, nil
		}
		if len(cmd.Args) == 1 && cmd.Args[0] == "--help" {
			return adapter.ProcessResult{Stdout: []byte("--print --output-format --conversation --mode --sandbox --new-project --disable-slash-commands --model")}, nil
		}
		return run(ctx, cmd)
	}}
}
func TestGovernedAntigravityRefusesUnqualifiedVersion(t *testing.T) {
	called := false
	for _, version := range []string{"1.2.13", "1.2.15", "1.3.2", "1.2.14 arbitrary"} {
		client := antigravity.NewGovernedProbe("agy", &mockRunner{runFn: func(_ context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
			if len(cmd.Args) == 1 && cmd.Args[0] == "--version" {
				return adapter.ProcessResult{Stdout: []byte(version)}, nil
			}
			called = true
			return adapter.ProcessResult{}, nil
		}})
		if _, err := client.Probe(t.Context()); !errors.Is(err, model.ErrUnavailable) || called {
			t.Fatalf("unqualified CLI contract accepted for version %s: %v", version, err)
		}
	}
	req := nativeRequest()
	req.Model = "gemini-3.8-flash-high"
	client := antigravity.NewGovernedProbe("agy", &mockRunner{runFn: func(context.Context, adapter.Command) (adapter.ProcessResult, error) {
		called = true
		return adapter.ProcessResult{}, nil
	}})
	if _, err := client.Run(t.Context(), req); !errors.Is(err, model.ErrUnavailable) || called {
		t.Fatal("unprobed contract launched")
	}
}

func TestGovernedAntigravityAcceptsSupportedRange(t *testing.T) {
	for _, version := range []string{"1.2.14", "1.4.0", "1.4.1", "1.4.2"} {
		t.Run(version, func(t *testing.T) {
			runner := governedRunnerWithVersion(version, func(ctx context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
				return adapter.ProcessResult{Stdout: []byte(`{"status":"SUCCESS","response":"MARSHAL_PROBE_OK"}`)}, nil
			})
			client := antigravity.NewGovernedProbe("agy", runner)
			probe, err := client.Probe(t.Context())
			if err != nil {
				t.Fatalf("supported version %s was rejected: %v", version, err)
			}
			if probe.Version != version {
				t.Fatalf("probe version reported dishonestly: got %q, want %q", probe.Version, version)
			}
			if probe.Capabilities["mcp_client"] != "unsupported" { // probe-only has no worker MCP
				t.Fatalf("probe acquired unexpected worker MCP capability")
			}
		})
	}
}

func TestGovernedAntigravityExtremeUnverifiedNewerEditsWithBoundMCP(t *testing.T) {
	const newerVersion = "1.5.0"
	t.Setenv("MARSHAL_MCP_BINARY", "/bin/true")
	root := t.TempDir()
	if err := os.Mkdir(root+"/.agents", 0700); err != nil {
		t.Fatal(err)
	}
	ctx, err := sessionmcp.WithBoundWorker(t.Context(), root, "TASK-newer", func(context.Context, io.Reader, io.Writer) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	var executedCommand adapter.Command
	called, boundMCP := false, false
	runner := &mockRunner{runFn: func(ctx context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
		if len(cmd.Args) == 1 && cmd.Args[0] == "--version" {
			return adapter.ProcessResult{Stdout: []byte(newerVersion)}, nil
		}
		if len(cmd.Args) == 1 && cmd.Args[0] == "--help" {
			return adapter.ProcessResult{Stdout: []byte("--print --output-format --conversation --mode --sandbox --new-project --disable-slash-commands --model")}, nil
		}
		called = true
		executedCommand = cmd
		req, e := sessionmcp.SandboxRequest(ctx, model.SandboxRequest{Worktree: root})
		if e != nil {
			t.Fatal(e)
		}
		for _, b := range req.ReadOnlyBinds {
			if b.Target == "/home/marshal/.gemini/config" {
				data, e := os.ReadFile(b.Source + "/mcp_config.json")
				boundMCP = e == nil && strings.Contains(string(data), "marshal") && strings.Contains(string(data), "relay")
			}
		}
		return adapter.ProcessResult{
			Stdout:    []byte(`{"status":"SUCCESS","conversation_id":"native-newer-session","response":"work completed"}`),
			ExitCode:  0,
			StartedAt: time.Now().UTC(),
			EndedAt:   time.Now().UTC().Add(time.Second),
		}, nil
	}}

	client := antigravity.NewGoverned("agy", runner)
	probe, err := client.Probe(ctx)
	if err != nil {
		t.Fatalf("unverified newer version blocked probe in Marshal: %v", err)
	}
	if probe.Version != newerVersion {
		t.Fatalf("probe did not report newer version honestly: got %q, want %q", probe.Version, newerVersion)
	}
	if probe.Capabilities["mcp_client"] != "native" {
		t.Fatalf("unverified newer governed worker should use the bound worker MCP, got %q", probe.Capabilities["mcp_client"])
	}

	req := nativeRequest()
	req.Worktree = root
	req.TaskID = "TASK-newer"
	req.Model = "gemini-3.8-flash-high"
	result, err := client.Run(ctx, req)
	if err != nil {
		t.Fatalf("unverified newer version blocked execution in Marshal: %v", err)
	}
	if !called {
		t.Fatal("runner was not invoked")
	}
	// A read-only plan mode denied every edit and command, so governed work
	// on a newer agy always ended blocked.
	if executedCommand.Args[0] != "--mode" || executedCommand.Args[1] != "accept-edits" {
		t.Fatalf("expected governed accept-edits mode, got %v", executedCommand.Args[:2])
	}
	if strings.Contains(strings.Join(executedCommand.Args, " "), "dangerously") || len(executedCommand.Env) != 0 {
		t.Fatal("permission bypass or arbitrary environment")
	}
	if !boundMCP {
		t.Fatal("unverified newer governed worker has no bound worker MCP")
	}
	if result.Status != adapter.StatusSuccess || result.FinalText != "work completed" {
		t.Fatalf("unexpected result: %+v", result)
	}
	evidence := client.CollectEvidence(result)
	if evidence["adapter_version"] != newerVersion {
		t.Fatalf("evidence adapter_version = %v, want %s", evidence["adapter_version"], newerVersion)
	}
	if evidence["unverified_version"] != true {
		t.Fatalf("evidence missing unverified_version flag: %v", evidence)
	}
	outcome, ok := evidence["process_outcome"].(map[string]any)
	if !ok || outcome["execution_mode"] != "native" {
		t.Fatalf("process_outcome missing execution_mode native: %v", outcome)
	}
}

func governedRunnerWithVersion(version string, run func(context.Context, adapter.Command) (adapter.ProcessResult, error)) *mockRunner {
	return &mockRunner{runFn: func(ctx context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
		if len(cmd.Args) == 1 && cmd.Args[0] == "--version" {
			return adapter.ProcessResult{Stdout: []byte(version)}, nil
		}
		if len(cmd.Args) == 1 && cmd.Args[0] == "--help" {
			return adapter.ProcessResult{Stdout: []byte("--print --output-format --conversation --mode --sandbox --new-project --disable-slash-commands --model")}, nil
		}
		return run(ctx, cmd)
	}}
}
