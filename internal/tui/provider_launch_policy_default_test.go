//go:build governed

package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/opencodecli"
)

// Community keeps each provider's own prompts: no tool pre-approval, approval
// policy, sandbox override or project trust is added to MARSHAL's launches.
func TestCommunityProviderLaunchesKeepProviderPrompts(t *testing.T) {
	saved := openCodeCLIContract
	openCodeCLIContract = func() (opencodecli.Contract, bool) {
		return opencodecli.Contract{Version: "2.1.4", Run: true, FormatJSON: true, Model: true, Standalone: true}, true
	}
	defer func() { openCodeCLIContract = saved }()
	root := t.TempDir()
	for _, provider := range []string{"codex", "claude", "opencode"} {
		t.Run(provider, func(t *testing.T) {
			kickoff := marshalKickoffArgs(provider, root)
			args, _, dir, err := configureMarshalMCP(provider, root, kickoff, nil, nil, "marshal", "")
			if err != nil {
				t.Fatal(err)
			}
			if dir != nil {
				defer dir.remove()
			}
			joined := strings.Join(args, " ")
			for _, banned := range []string{"default_tools_approval_mode", "approval_policy", "sandbox_mode", "--allowedTools", "trust_level", " -p "} {
				if strings.Contains(" "+joined+" ", banned) {
					t.Fatalf("%s launch carries %q in Community: %v", provider, banned, args)
				}
			}
			if provider == "claude" {
				cfg := slices.Index(args, "--mcp-config")
				if cfg < 0 || len(args) <= cfg+2 || args[cfg+2] != "--" {
					t.Fatalf("claude --mcp-config not terminated: %v", args)
				}
			}
			// The per-launch OpenCode configuration only reaches a 2.x CLI that
			// runs its own server; that is correctness, not an approval change.
			if provider == "opencode" && (len(args) == 0 || args[0] != "--standalone") {
				t.Fatalf("2.x OpenCode launch not standalone: %v", args)
			}
		})
	}
}
