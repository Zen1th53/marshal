//go:build !governed

package tui

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/constitution"
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
	if err != nil || !strings.Contains(brief, "Codex, Claude and OpenCode support governed tasks") || !strings.Contains(brief, "acceptance mode marshal (the Marshal leads).") || !strings.HasSuffix(brief, buildRoleAuthority) {
		t.Fatalf("private run briefing invalid: %v", err)
	}
}

// The shipped protocol ends with the authority protocol and the briefing ends
// with it too; the Marshal must receive it once, last, not twice.
func TestExtremeBriefingCarriesAuthorityOnce(t *testing.T) {
	protocol, err := constitution.MarshalProtocol()
	if err != nil || !strings.HasSuffix(protocol, buildRoleAuthority) {
		t.Fatalf("protocol no longer ends with the authority protocol: %v", err)
	}
	brief, err := marshalRoleBriefing([]string{"codex", "claude"}, marshal.DefaultSettings(), marshal.Standard)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(brief, buildRoleAuthority); n != 1 || !strings.HasSuffix(brief, buildRoleAuthority) {
		t.Fatalf("authority protocol delivered %d times", n)
	}
	if !strings.HasPrefix(brief, strings.TrimSuffix(protocol, buildRoleAuthority)) {
		t.Fatal("briefing no longer opens with the whole base protocol")
	}
}
