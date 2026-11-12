//go:build !governed

package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/statepath"
)

func TestExtremeMarshalMCPLaunchConfig(t *testing.T) {
	providers := []string{"codex", "claude", "opencode", "antigravity"}

	for _, provider := range providers {
		t.Run(provider, func(t *testing.T) {
			root := t.TempDir()
			protocol := "MARSHAL PROTOCOL\nTest extreme instructions"

			args, env, dir, err := prepareMarshalLaunch(provider, root, nil, protocol, false, "")
			if err != nil {
				t.Fatalf("%s prepareMarshalLaunch failed: %v", provider, err)
			}
			if dir != nil {
				defer dir.remove()
			}

			switch provider {
			case "codex":
				// Codex should have -c mcp_servers.marshal.command and -c mcp_servers.marshal.args
				hasCmd, hasArgs := false, false
				for i, arg := range args {
					if arg == "-c" && i+1 < len(args) {
						val := args[i+1]
						if strings.HasPrefix(val, "mcp_servers.marshal.command=") {
							hasCmd = true
						}
						if strings.HasPrefix(val, "mcp_servers.marshal.args=") {
							hasArgs = true
							if !strings.Contains(val, `"--role"`) || !strings.Contains(val, `"marshal"`) {
								t.Errorf("codex mcp args missing marshal role: %s", val)
							}
						}
					}
				}
				if !hasCmd || !hasArgs {
					t.Fatalf("codex args missing MCP server flags: %v", args)
				}

			case "claude":
				// Claude should have --mcp-config flag pointing to existing file
				var cfgFile string
				for i, arg := range args {
					if arg == "--mcp-config" && i+1 < len(args) {
						cfgFile = args[i+1]
						break
					}
				}
				if cfgFile == "" {
					t.Fatalf("claude args missing --mcp-config: %v", args)
				}
				if len(args) < 4 || args[len(args)-3] != cfgFile || args[len(args)-2] != "--" || args[len(args)-1] != "Start." {
					t.Fatalf("claude opening was consumed by variadic --mcp-config: %v", args)
				}
				data, err := os.ReadFile(cfgFile)
				if err != nil {
					t.Fatalf("failed to read claude mcp config %s: %v", cfgFile, err)
				}
				var cfg struct {
					MCPServers map[string]struct {
						Command string   `json:"command"`
						Args    []string `json:"args"`
					} `json:"mcpServers"`
				}
				if err := json.Unmarshal(data, &cfg); err != nil {
					t.Fatalf("failed to parse claude mcp config: %v", err)
				}
				server, ok := cfg.MCPServers["marshal"]
				if !ok {
					t.Fatalf("claude mcp config missing 'marshal' server")
				}
				if len(server.Args) == 0 || server.Args[len(server.Args)-1] != "marshal" {
					t.Errorf("claude mcp args missing role marshal: %v", server.Args)
				}

			case "opencode":
				// OpenCode should have OPENCODE_CONFIG_CONTENT in env
				var configContent string
				for _, e := range env {
					if strings.HasPrefix(e, "OPENCODE_CONFIG_CONTENT=") {
						configContent = strings.TrimPrefix(e, "OPENCODE_CONFIG_CONTENT=")
						break
					}
				}
				if configContent == "" {
					t.Fatalf("opencode missing OPENCODE_CONFIG_CONTENT in env: %v", env)
				}
				var cfg struct {
					MCP map[string]struct {
						Type    string   `json:"type"`
						Command []string `json:"command"`
					} `json:"mcp"`
				}
				if err := json.Unmarshal([]byte(configContent), &cfg); err != nil {
					t.Fatalf("failed to parse opencode config: %v", err)
				}
				server, ok := cfg.MCP["marshal"]
				if !ok || server.Type != "local" {
					t.Fatalf("opencode config missing local 'marshal' server: %v", cfg)
				}
				if len(server.Command) == 0 || server.Command[len(server.Command)-1] != "marshal" {
					t.Errorf("opencode command missing role marshal: %v", server.Command)
				}

			case "antigravity":
				for _, value := range env {
					if strings.HasPrefix(value, "HOME=") {
						t.Fatal("agy authentication home overridden")
					}
				}
				if _, err := os.Stat(filepath.Join(dir.path, "mcp_config.json")); !os.IsNotExist(err) {
					t.Fatal("unsupported agy MCP config must not be fabricated")
				}

			}

			// Verify cleanup: when dir.remove() is called, dir.path must not exist
			if dir != nil {
				p := dir.path
				dir.remove()
				if _, err := os.Stat(p); !os.IsNotExist(err) {
					t.Fatalf("dir %s was not cleaned up after remove()", p)
				}
			}
		})
	}
}

func TestExtremeClaudeMCPConfigPreservesSavedIntakeOpening(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, statepath.DirName), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, statepath.DirName, "marshal-intake.json"), []byte(`{"language":"Uzbek"}`), 0600); err != nil {
		t.Fatal(err)
	}
	args, _, dir, err := prepareMarshalLaunch("claude", root, nil, "MARSHAL PROTOCOL", false, "")
	if dir != nil {
		defer dir.remove()
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(args) < 4 || args[len(args)-4] != "--mcp-config" || args[len(args)-2] != "--" || args[len(args)-1] != "Continue." {
		t.Fatalf("saved intake opening was consumed by variadic --mcp-config: %v", args)
	}
}

func TestExtremeWorkerMCPLaunchConfig(t *testing.T) {
	providers := []string{"codex", "claude", "opencode", "antigravity", "agy"}
	taskID := "TASK-worker-test-42"

	for _, provider := range providers {
		t.Run(provider, func(t *testing.T) {
			root := t.TempDir()
			args, env, cleanup, err := ConfigureWorkerMCP(provider, root, taskID, nil, nil)
			if err != nil {
				t.Fatalf("%s ConfigureWorkerMCP failed: %v", provider, err)
			}
			defer cleanup()

			switch provider {
			case "codex":
				foundWorker, foundTask := false, false
				for _, arg := range args {
					if strings.Contains(arg, `"worker"`) {
						foundWorker = true
					}
					if strings.Contains(arg, taskID) {
						foundTask = true
					}
				}
				if !foundWorker || !foundTask {
					t.Fatalf("codex worker missing worker or task flag: %v", args)
				}

			case "claude":
				var cfgFile string
				for i, arg := range args {
					if arg == "--mcp-config" && i+1 < len(args) {
						cfgFile = args[i+1]
						break
					}
				}
				data, err := os.ReadFile(cfgFile)
				if err != nil {
					t.Fatalf("failed to read claude config: %v", err)
				}
				content := string(data)
				if !strings.Contains(content, `"worker"`) || !strings.Contains(content, taskID) {
					t.Fatalf("claude worker config missing role or task: %s", content)
				}

			case "opencode":
				var configContent string
				for _, e := range env {
					if strings.HasPrefix(e, "OPENCODE_CONFIG_CONTENT=") {
						configContent = strings.TrimPrefix(e, "OPENCODE_CONFIG_CONTENT=")
						break
					}
				}
				if !strings.Contains(configContent, `"worker"`) || !strings.Contains(configContent, taskID) {
					t.Fatalf("opencode worker config missing role or task: %s", configContent)
				}

			case "antigravity", "agy":
				for _, value := range env {
					if strings.HasPrefix(value, "HOME=") {
						t.Fatal("agy authentication home overridden")
					}
				}

			}
		})
	}
}

func TestExtremeMCPNeverEditsUserGlobalConfig(t *testing.T) {
	// Setup mock home with existing global config
	mockHome := t.TempDir()
	t.Setenv("HOME", mockHome)

	geminiConfigDir := filepath.Join(mockHome, ".gemini", "config")
	if err := os.MkdirAll(geminiConfigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	initialGlobalConfig := `{"mcpServers":{"user-global":{"command":"some-cmd"}}}`
	globalPath := filepath.Join(geminiConfigDir, "mcp_config.json")
	if err := os.WriteFile(globalPath, []byte(initialGlobalConfig), 0o600); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	// Run launch configuration for all providers
	for _, provider := range []string{"codex", "claude", "opencode", "antigravity"} {
		_, _, dir, err := prepareMarshalLaunch(provider, root, nil, "MARSHAL PROTOCOL\nTest", false, "")
		if err != nil {
			t.Fatalf("%s launch failed: %v", provider, err)
		}
		if dir != nil {
			dir.remove()
		}

		_, _, cleanup, err := ConfigureWorkerMCP(provider, root, "TASK-1", nil, nil)
		if err != nil {
			t.Fatalf("%s worker launch failed: %v", provider, err)
		}
		cleanup()
	}

	// Assert the user's global config has NOT been modified
	data, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != initialGlobalConfig {
		t.Fatalf("user's global mcp_config.json was modified!\nExpected:\n%s\nGot:\n%s", initialGlobalConfig, string(data))
	}

	// Assert no global claude or opencode config files were created in HOME
	if _, err := os.Stat(filepath.Join(mockHome, ".claude.json")); !os.IsNotExist(err) {
		t.Fatal("unexpected .claude.json created in user HOME")
	}
	if _, err := os.Stat(filepath.Join(mockHome, ".config", "opencode")); !os.IsNotExist(err) {
		t.Fatal("unexpected opencode config created in user HOME")
	}
}

func TestExtremeMCPWithFakeProviderCLIs(t *testing.T) {
	fakeProviderCLIs(t, "codex", "claude", "agy", "opencode", "marshal")
	root := t.TempDir()

	for _, provider := range []string{"codex", "claude", "agy", "opencode"} {
		args, env, dir, err := prepareMarshalLaunch(provider, root, nil, "MARSHAL PROTOCOL\nTest", false, "")
		if err != nil {
			t.Fatalf("%s launch failed with fake CLIs: %v", provider, err)
		}
		if dir != nil {
			dir.remove()
		}
		if len(args) == 0 {
			t.Fatalf("%s args empty", provider)
		}

		wArgs, wEnv, cleanup, err := ConfigureWorkerMCP(provider, root, "TASK-123", nil, nil)
		if err != nil {
			t.Fatalf("%s worker launch failed with fake CLIs: %v", provider, err)
		}
		cleanup()
		_ = wArgs
		_ = wEnv
		_ = env
	}
}

func TestExtremeWorkerActualLaunch(t *testing.T) {
	for _, provider := range []string{"codex", "claude", "opencode"} {
		t.Run(provider, func(t *testing.T) {
			root := t.TempDir()
			script := filepath.Join(root, provider)
			capture := filepath.Join(root, "capture")
			t.Setenv("MCP_CAPTURE", capture)
			if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$MCP_CAPTURE\"\nenv >> \"$MCP_CAPTURE\"\nwhile [ \"$#\" -gt 0 ]; do if [ \"$1\" = --mcp-config ]; then shift; cat \"$1\" >> \"$MCP_CAPTURE\"; fi; shift; done\n"), 0700); err != nil {
				t.Fatal(err)
			}
			var native driver.Native
			switch provider {
			case "codex":
				native = driver.Codex(script)
			case "claude":
				native = driver.Claude(script)
			case "opencode":
				native = driver.OpenCode(script)
			}
			agent := &activeTmuxAgent{canonicalTaskID: "TASK-canonical"}
			configured := configureWorkerDriverExtreme(native, root, agent)
			h, err := configured.Launch(context.Background(), driver.Request{Task: marshal.Task{Worker: provider, PlanTaskID: "plan-local", BaseCommit: "base"}, Worktree: root, Brief: "work"})
			if err != nil {
				t.Fatal(err)
			}
			<-h.Done()
			data, err := os.ReadFile(capture)
			if err != nil {
				t.Fatal(err)
			}
			text := string(data)
			if provider == "claude" {
				var config string
				lines := strings.Split(text, "\n")
				for i, value := range lines {
					if value == "--mcp-config" && i+1 < len(lines) {
						config = lines[i+1]
					}
				}
				// Config is removed when the process exits; its path proves actual dispatch.
				if !strings.Contains(text, "TASK-canonical") || !strings.Contains(text, "worker") {
					t.Fatal(text)
				}
				if config == "" {
					t.Fatal(text)
				}
				if _, err := os.Stat(config); !os.IsNotExist(err) {
					t.Fatal("worker config survived exit")
				}
			} else if !strings.Contains(text, "TASK-canonical") || !strings.Contains(text, "worker") {
				t.Fatal(text)
			}
			if strings.Contains(text, "XDG_CONFIG_HOME="+root) {
				t.Fatal("global settings masked")
			}
		})
	}
}
func TestExtremeMCPExecutableIsCurrentBuild(t *testing.T) {
	fakeProviderCLIs(t, "marshal")
	t.Setenv("MARSHAL_MCP_BINARY", "")
	current, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if marshalMCPBinary() != current {
		t.Fatal("selected another build from PATH")
	}
}
func TestExtremeOpenCodeMCPMerge(t *testing.T) {
	env, err := configureOpenCodeMCP(nil, t.TempDir(), []string{`OPENCODE_CONFIG_CONTENT={"model":"keep","mcp":{"existing":{"type":"local","command":["keep"]}}}`}, "worker", "TASK-1")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(env, "\n")
	for _, expected := range []string{`"model":"keep"`, `"existing"`, `"marshal"`} {
		if !strings.Contains(text, expected) {
			t.Fatal(text)
		}
	}
	if _, err := configureOpenCodeMCP(nil, "", []string{"OPENCODE_CONFIG_CONTENT=bad"}, "marshal", ""); err == nil {
		t.Fatal("invalid settings silently discarded")
	}
}

func TestExtremeWorkerMCPPreservesRuntimeLease(t *testing.T) {
	native := driver.Codex("/owned/provider")
	called := false
	native.ConfigureLaunch = func(_ context.Context, _ driver.Request, args, env []string) ([]string, []string, func(), error) {
		called = true
		return args, append(env, "LEASE=runtime-owned"), func() {}, nil
	}
	configured := configureWorkerDriverExtreme(native, t.TempDir(), &activeTmuxAgent{}).(driver.Native)
	_, env, cleanup, err := configured.ConfigureLaunch(t.Context(), driver.Request{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if !called || len(env) != 1 || env[0] != "LEASE=runtime-owned" {
		t.Fatal("TUI replaced the runtime's lease-bound launch")
	}
}
