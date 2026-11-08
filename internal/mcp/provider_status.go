package mcp

import (
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/auth"
)

// ProviderStatus is the session-MCP state of one provider. The session
// transport is stdio launched per session, so it does not depend on whether
// the optional HTTP server is running.
type ProviderStatus struct {
	Provider    string `json:"provider"`
	Status      string `json:"status"` // "ready", "connected", "fallback"
	Transport   string `json:"transport"`
	ActiveToken string `json:"active_token,omitempty"`
	Detail      string `json:"detail"`
}

// CheckProviderStates reports how each provider reaches MARSHAL memory.
// A live per-session token means a session is currently using the transport.
// The authentication manager may be nil.
func CheckProviderStates(authMgr *auth.Manager) map[string]ProviderStatus {
	active := map[string]string{}
	if authMgr != nil {
		if tokens, err := authMgr.ListTokens(); err == nil {
			for _, token := range tokens {
				if token.Revoked || token.Kind != auth.KindMCPClient {
					continue
				}
				name := strings.ToLower(token.Name)
				for _, provider := range []string{"claude", "codex", "opencode"} {
					if active[provider] == "" && strings.HasPrefix(name, "mcp-"+provider) {
						active[provider] = token.ID
					}
				}
			}
		}
	}
	states := map[string]ProviderStatus{}
	for provider, mechanism := range map[string]string{"claude": "--mcp-config", "codex": "-c mcp_servers.marshal", "opencode": "OPENCODE_CONFIG_CONTENT"} {
		status := ProviderStatus{Provider: provider, Status: "ready", Transport: "stdio", Detail: fmt.Sprintf("ready via %s (session-only stdio, memory read and candidate write)", mechanism)}
		if id := active[provider]; id != "" {
			status.Status, status.ActiveToken = "connected", id
			status.Detail = fmt.Sprintf("connected via %s (active session token: %s)", mechanism, id)
		}
		states[provider] = status
	}
	states["antigravity"] = ProviderStatus{Provider: "antigravity", Status: "fallback", Transport: "none", Detail: "fallback to CLI memory commands (per-run MCP unsupported)"}
	return states
}
