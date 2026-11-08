//go:build unix

package sessionmcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/proposalmcp"
)

const openCodeConfigPrefix = "OPENCODE_CONFIG_CONTENT="
const maxOpenCodeConfigBytes = 64 << 10

// Only the owning runner's trusted runtime config and this launch's generated
// relay are combined. Host environment and arbitrary provider Command.Env are
// never configuration sources for the governed sandbox.
func openCodeSandboxEnvironment(runtimeEnv, relayEnv []string) ([]string, error) {
	config, env, err := openCodeEnvironmentConfig(runtimeEnv)
	if err != nil {
		return nil, err
	}
	relay, _, err := openCodeEnvironmentConfig(relayEnv)
	if err != nil {
		return nil, err
	}
	servers, err := openCodeMCPServers(config)
	if err != nil {
		return nil, err
	}
	var generated map[string]json.RawMessage
	if err := proposalmcp.DecodeStrict(relay["mcp"], &generated); err != nil || generated == nil || len(generated["marshal"]) == 0 {
		return nil, fmt.Errorf("OpenCode generated Marshal relay is unavailable")
	}
	// A stale same-name server can never supersede this exact launch's relay.
	// Every unrelated runtime setting/server retains its original JSON value.
	servers["marshal"] = generated["marshal"]
	raw, err := json.Marshal(servers)
	if err != nil {
		return nil, err
	}
	config["mcp"] = raw
	raw, err = json.Marshal(config)
	if err != nil {
		return nil, err
	}
	if len(raw) > maxOpenCodeConfigBytes {
		return nil, fmt.Errorf("OpenCode merged configuration exceeds bound")
	}
	return append(env, openCodeConfigPrefix+string(raw)), nil
}

func openCodeMCPServers(config map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	servers := map[string]json.RawMessage{}
	if raw, exists := config["mcp"]; exists {
		if err := proposalmcp.DecodeStrict(raw, &servers); err != nil || servers == nil {
			return nil, fmt.Errorf("OpenCode MCP configuration must be an object")
		}
	}
	return servers, nil
}

func openCodeEnvironmentConfig(env []string) (map[string]json.RawMessage, []string, error) {
	config := map[string]json.RawMessage{}
	retained := make([]string, 0, len(env))
	seen := false
	for _, entry := range env {
		if !strings.HasPrefix(entry, openCodeConfigPrefix) {
			retained = append(retained, entry)
			continue
		}
		if seen {
			return nil, nil, fmt.Errorf("OpenCode configuration environment is ambiguous")
		}
		seen = true
		raw := []byte(strings.TrimPrefix(entry, openCodeConfigPrefix))
		if len(raw) > maxOpenCodeConfigBytes {
			return nil, nil, fmt.Errorf("OpenCode configuration exceeds bound")
		}
		if err := proposalmcp.DecodeStrict(raw, &config); err != nil || config == nil {
			return nil, nil, fmt.Errorf("OpenCode configuration must be a strict JSON object")
		}
	}
	return config, retained, nil
}
