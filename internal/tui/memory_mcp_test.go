package tui

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/auth"
)

func TestNativeMemoryMCPUsesOneStdioSessionAndRevokes(t *testing.T) {
	for _, provider := range []string{"claude", "codex", "opencode"} {
		t.Run(provider, func(t *testing.T) {
			root := t.TempDir()
			kickoff := marshalKickoffArgs(provider, root)
			args, env, dir, err := configureMarshalMCP(provider, root, kickoff, nil, nil, "session", "session-1")
			if err != nil {
				t.Fatal(err)
			}
			defer dir.remove()
			if dir.mcpToken == "" {
				t.Fatal("session token missing")
			}
			manager := dir.authMgr
			if _, err := manager.Authenticate(dir.mcpToken); err != nil {
				t.Fatal(err)
			}
			switch provider {
			case "claude":
				index := -1
				for i, arg := range args {
					if arg == "--mcp-config" {
						if index >= 0 {
							t.Fatal("duplicate MCP config")
						}
						index = i
					}
				}
				if index < 0 || len(args) <= index+2 || args[index+2] != "--" {
					t.Fatalf("Claude kickoff may be swallowed by variadic config: %v", args)
				}
				data, err := os.ReadFile(args[index+1])
				if err != nil {
					t.Fatal(err)
				}
				var cfg struct {
					Servers map[string]struct {
						Args []string          `json:"args"`
						Env  map[string]string `json:"env"`
					} `json:"mcpServers"`
				}
				if err := json.Unmarshal(data, &cfg); err != nil {
					t.Fatal(err)
				}
				server := cfg.Servers["marshal"]
				if len(server.Args) < 2 || server.Args[1] != "stdio" || server.Env["MARSHAL_MCP_TOKEN"] != dir.mcpToken {
					t.Fatal("Claude did not receive scoped stdio config")
				}
			case "codex":
				joined := strings.Join(args, " ")
				if strings.Count(joined, "mcp_servers.marshal.command=") != 1 || !strings.Contains(joined, "stdio") || !strings.Contains(joined, "env_vars=") {
					t.Fatalf("Codex config: %v", args)
				}
			case "opencode":
				if !strings.Contains(strings.Join(env, " "), `"type":"local"`) || !strings.Contains(strings.Join(env, " "), `"stdio"`) || !strings.Contains(strings.Join(env, " "), `"MARSHAL_MCP_TOKEN"`) {
					t.Fatalf("OpenCode config: %v", env)
				}
			}
			token := dir.mcpToken
			dir.remove()
			if _, err := manager.Authenticate(token); err == nil {
				t.Fatal("launch cleanup retained token")
			}
			if _, err := os.Stat(dir.path); !os.IsNotExist(err) {
				t.Fatal("launch cleanup retained config")
			}
			if _, err := auth.NewManager(root).Authenticate(token); err == nil {
				t.Fatal("credential escaped project namespace")
			}
		})
	}
}
