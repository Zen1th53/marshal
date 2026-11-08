//go:build unix

package sessionmcp_test

import (
	"context"
	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/adapter/claude"
	"github.com/Zen1th53/marshal/internal/adapter/codex"
	"github.com/Zen1th53/marshal/internal/adapter/opencode"
	"github.com/Zen1th53/marshal/internal/sessionmcp"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type providerRunner struct{ configPath string }

func (r *providerRunner) Run(ctx context.Context, spec adapter.Command) (adapter.ProcessResult, error) {
	for i, arg := range spec.Args {
		if arg == "--mcp-config" && i+1 < len(spec.Args) {
			r.configPath = spec.Args[i+1]
		}
	}
	cmd := exec.CommandContext(ctx, spec.Path, spec.Args...)
	cmd.Env = append(os.Environ(), spec.Env...)
	cmd.Dir = spec.Dir
	cmd.Stdin = strings.NewReader(string(spec.Stdin))
	out, err := cmd.Output()
	return adapter.ProcessResult{Stdout: out}, err
}
func TestGovernedProviderActualMCPLaunch(t *testing.T) {
	for _, provider := range []string{"codex", "claude", "opencode"} {
		t.Run(provider, func(t *testing.T) {
			root := t.TempDir()
			bin := filepath.Join(root, provider)
			capture := filepath.Join(root, "captured")
			t.Setenv("MCP_CAPTURE", capture)
			script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$MCP_CAPTURE\"\nenv >> \"$MCP_CAPTURE\"\nwhile [ \"$#\" -gt 0 ]; do if [ \"$1\" = --mcp-config ]; then shift; cat \"$1\" >> \"$MCP_CAPTURE\"; fi; shift; done\nprintf '{}\\n'\n"
			if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			runner := &providerRunner{}
			var client adapter.Adapter
			switch provider {
			case "codex":
				client = codex.NewWithModels(bin, runner, []codex.ModelInfo{{Slug: "test"}}, "test")
			case "claude":
				client = claude.New(bin, runner)
			case "opencode":
				client = opencode.New(bin, runner)
			}
			ctx := sessionmcp.WithWorker(context.Background(), root, "TASK-canonical", "LEASE-fixture")
			if _, err := client.Run(ctx, adapter.Request{TaskID: "TASK-canonical", Title: "work", Worktree: root}); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(capture)
			if err != nil {
				t.Fatal(err)
			}
			for _, expected := range []string{"relay", "--requests", "--responses"} {
				if !strings.Contains(string(data), expected) {
					t.Fatalf("missing %s: %s", expected, data)
				}
			}
			if runner.configPath != "" {
				if _, err := os.Stat(runner.configPath); !os.IsNotExist(err) {
					t.Fatal("session config survived")
				}
			}
		})
	}
}
