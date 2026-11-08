//go:build !governed

package tui

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

func TestExtremeRunBriefUsesImplementedAuthority(t *testing.T) {
	instructions := marshalRunAuthorityInstructions(marshal.DefaultSettings())
	for _, forbidden := range []string{"press A", "only the operator", "opencode support native only", "AGY supports native only"} {
		if strings.Contains(instructions.egress+instructions.workerModes+instructions.settings+instructions.control, forbidden) {
			t.Fatalf("obsolete private run instruction: %q", forbidden)
		}
	}
	brief, err := marshalRoleBriefing([]string{"codex", "claude", "opencode"}, marshal.DefaultSettings(), marshal.Ultra)
	if err != nil || !strings.Contains(brief, "Codex, Claude and OpenCode support governed tasks") || !strings.Contains(brief, "acceptance mode marshal.") || !strings.HasSuffix(brief, buildRoleAuthority) {
		t.Fatalf("private run briefing invalid: %v", err)
	}
}
