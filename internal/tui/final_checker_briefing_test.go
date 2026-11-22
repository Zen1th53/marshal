package tui

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// The planning briefing no longer reserves a final checker: the final check
// runs in a fresh session, so the Marshal may give tasks to any worker,
// Claude Code included.
func TestPlanningBriefingKeepsNoProviderOffTasks(t *testing.T) {
	for _, tier := range []marshal.Tier{marshal.Standard, marshal.Ultra} {
		brief, err := marshalRoleBriefing([]string{"codex", "claude"}, marshal.DefaultSettings(), tier)
		if err != nil {
			t.Fatal(err)
		}
		for _, banned := range []string{"Do not assign any task to", "final checker", "kept free", "Leave at least one of"} {
			if strings.Contains(brief, banned) {
				t.Fatalf("%s briefing reserves a checker (%q)", tier, banned)
			}
		}
		if !strings.Contains(brief, "Workers you may assign tasks to: codex, claude.") {
			t.Fatalf("%s briefing lost the worker list", tier)
		}
	}
}
