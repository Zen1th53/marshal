package sessionmcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMarshalMCPInvocationConfiguration(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MARSHAL_MCP_BINARY", binary)
	root := t.TempDir()
	marker := filepath.Join(root, ".mcp.json")
	if err := os.WriteFile(marker, []byte("existing project configuration"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"codex", "claude", "agy"} {
		t.Run(provider, func(t *testing.T) {
			args, env, cleanup, err := ConfigureMarshal(provider, root, []string{"exec", "prompt"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			var config string
			if provider == "codex" {
				if args[len(args)-1] != "prompt" {
					t.Fatal("MCP flags replaced the prompt")
				}
				if !strings.Contains(strings.Join(args, " "), "--role") {
					t.Fatal("missing fixed role")
				}
			}
			if provider == "claude" {
				for i, arg := range args {
					if arg == "--mcp-config" {
						config = args[i+1]
					}
				}
				info, err := os.Stat(config)
				if err != nil || info.Mode().Perm() != 0600 {
					t.Fatal("session config is not private")
				}
				data, err := os.ReadFile(config)
				if err != nil || !strings.Contains(string(data), root) {
					t.Fatal("missing project binding")
				}
			}
			if provider == "agy" {
				if len(env) != 1 || !strings.HasPrefix(env[0], "MARSHAL_KNOWLEDGE_MCP=") {
					t.Fatal("missing bound shell transport")
				}
				var command []string
				if err := json.Unmarshal([]byte(strings.TrimPrefix(env[0], "MARSHAL_KNOWLEDGE_MCP=")), &command); err != nil || len(command) == 0 {
					t.Fatal("invalid transport argv")
				}
			}
			cleanup()
			if config != "" {
				if _, err := os.Stat(config); !os.IsNotExist(err) {
					t.Fatal("invocation config survived cleanup")
				}
			}
			data, err := os.ReadFile(marker)
			if err != nil || string(data) != "existing project configuration" {
				t.Fatal("provider project config changed")
			}
		})
	}
}

func TestMarshalProviderMatchesConfigureMarshal(t *testing.T) {
	t.Setenv("MARSHAL_MCP_BINARY", "/bin/sh")
	for _, provider := range []string{"codex", "claude", "agy", "antigravity", "opencode", "unknown"} {
		_, _, cleanup, err := ConfigureMarshal(provider, t.TempDir(), []string{"x"}, nil)
		cleanup()
		if (err == nil) != MarshalProvider(provider) {
			t.Fatalf("%s: MarshalProvider=%v but ConfigureMarshal err=%v", provider, MarshalProvider(provider), err)
		}
	}
}
