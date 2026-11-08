//go:build !governed && linux

package sessionmcp_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/adapter/opencode"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/sandbox"
	"github.com/Zen1th53/marshal/internal/sessionmcp"
	"github.com/Zen1th53/marshal/internal/worker"
)

type configCaptureProcess struct {
	command adapter.Command
	calls   int
}

func (p *configCaptureProcess) Run(_ context.Context, c adapter.Command) (adapter.ProcessResult, error) {
	p.command = c
	p.calls++
	return adapter.ProcessResult{Stdout: []byte(`{"type":"text","part":{"text":"captured"}}`)}, nil
}

func TestOpenCodeWorkerKeepsTrustedRuntimeProviderAndExactRelay(t *testing.T) {
	tree := t.TempDir()
	t.Setenv("OPENCODE_CONFIG_CONTENT", `{"provider":{"host-import":{"npm":"forbidden"}}}`)
	trusted := `{"provider":{"ollama":{"npm":"@ai-sdk/openai-compatible","options":{"baseURL":"http://127.0.0.1:11434/v1"},"models":{"qwythos-9b":{"name":"qwythos-9b"}}}},"mcp":{"other":{"type":"local","command":["/bin/true"]},"marshal":{"type":"local","command":["/forbidden/stale-relay"]}}}`
	process := &configCaptureProcess{}
	runner := worker.NewSandboxed(process, sandbox.NewBwrap("/usr/bin/bwrap"), model.SandboxRequest{Worktree: tree, ExtraEnv: []string{"OPENCODE_CONFIG_CONTENT=" + trusted, "UNCHANGED_RUNTIME=value"}})
	ctx := sessionmcp.WithWorker(t.Context(), tree, "TASK-local-provider", "LEASE-fixture")
	if _, err := opencode.NewWithModel("/bin/true", runner, "ollama/qwythos-9b").Run(ctx, adapter.Request{TaskID: "TASK-local-provider", Title: "capture only", Worktree: tree}); err != nil {
		t.Fatal(err)
	}
	var content string
	count := 0
	for _, kv := range process.command.Env {
		if strings.HasPrefix(kv, "OPENCODE_CONFIG_CONTENT=") {
			content = strings.TrimPrefix(kv, "OPENCODE_CONFIG_CONTENT=")
			count++
		}
	}
	var merged map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &merged); err != nil {
		t.Fatal(err)
	}
	if len(merged["provider"]) == 0 || !strings.Contains(string(merged["provider"]), "qwythos-9b") || !strings.Contains(string(merged["provider"]), "http://127.0.0.1:11434/v1") {
		t.Fatalf("bound worker erased trusted local provider configuration: %s", content)
	}
	if count != 1 {
		t.Fatalf("ambiguous config environment has %d entries", count)
	}
	var servers map[string]struct {
		Command []string `json:"command"`
	}
	if err := json.Unmarshal(merged["mcp"], &servers); err != nil {
		t.Fatal(err)
	}
	if len(servers["other"].Command) != 1 || servers["other"].Command[0] != "/bin/true" {
		t.Fatal("trusted unrelated MCP config changed")
	}
	joined := strings.Join(servers["marshal"].Command, " ")
	if !strings.Contains(joined, "mcp relay --requests") || !strings.Contains(joined, "--responses") || strings.Contains(joined, "stale-relay") {
		t.Fatalf("relay is not current launch: %s", joined)
	}
	if strings.Contains(content, "host-import") {
		t.Fatal("host configuration imported")
	}
	if !strings.Contains(strings.Join(process.command.Env, "\n"), "UNCHANGED_RUNTIME=value") {
		t.Fatal("unrelated trusted runtime environment changed")
	}
}

func TestOpenCodeWorkerRefusesMalformedOrAmbiguousConfigurationBeforeLaunch(t *testing.T) {
	invalid := map[string][]string{
		"malformed":        {"OPENCODE_CONFIG_CONTENT={"},
		"null":             {"OPENCODE_CONFIG_CONTENT=null"},
		"array":            {"OPENCODE_CONFIG_CONTENT=[]"},
		"duplicate-env":    {"OPENCODE_CONFIG_CONTENT={}", "OPENCODE_CONFIG_CONTENT={}"},
		"duplicate-root":   {`OPENCODE_CONFIG_CONTENT={"provider":{},"provider":{}}`},
		"duplicate-nested": {`OPENCODE_CONFIG_CONTENT={"provider":{"ollama":{"models":{},"models":{}}}}`},
		"trailing":         {"OPENCODE_CONFIG_CONTENT={} {}"},
		"invalid-mcp":      {`OPENCODE_CONFIG_CONTENT={"mcp":[]}`},
		"null-mcp":         {`OPENCODE_CONFIG_CONTENT={"mcp":null}`},
		"oversized":        {`OPENCODE_CONFIG_CONTENT={"large":"` + strings.Repeat("x", 64<<10) + `"}`},
	}
	for _, source := range []string{"runtime", "configure"} {
		for name, env := range invalid {
			t.Run(source+"/"+name, func(t *testing.T) {
				tree := t.TempDir()
				ctx := sessionmcp.WithWorker(t.Context(), tree, "TASK-refuse-config", "LEASE-fixture")
				if source == "configure" {
					_, _, cleanup, err := sessionmcp.Configure(ctx, "opencode", []string{"run"}, env)
					defer cleanup()
					if err == nil {
						t.Fatal("ambiguous source config accepted")
					}
					return
				}
				process := &configCaptureProcess{}
				runner := worker.NewSandboxed(process, sandbox.NewBwrap("/usr/bin/bwrap"), model.SandboxRequest{Worktree: tree, ExtraEnv: env})
				_, err := opencode.NewWithModel("/bin/true", runner, "ollama/qwythos-9b").Run(ctx, adapter.Request{TaskID: "TASK-refuse-config", Title: "must not launch", Worktree: tree})
				if err == nil || process.calls != 0 {
					t.Fatalf("invalid runtime config launched: error=%v calls=%d", err, process.calls)
				}
			})
		}
	}
}
