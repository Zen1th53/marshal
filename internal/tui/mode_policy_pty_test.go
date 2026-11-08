//go:build linux

package tui

import (
	"github.com/Zen1th53/marshal/internal/statepath"
	"testing"
)

// Mode changes grant no authority: Community still requires verified entitlement,
// and Extreme cannot restart explicitly stopped execution by changing its label.
func TestPTYModeIsAPreferenceNotAnAuthority(t *testing.T) {
	bin := buildMarshalBinary(t)
	sweepWorkEnvironment(t)
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	project := initProject(t, bin)
	t.Chdir(project)
	terminal := startFrozenTUIInProject(t, 40, 200, bin, project, "tui", "SESSION-pty-mode")
	terminal.mustSee("Marshal")
	if statepath.PublicEngine {
		terminal.mustSee("ULTRA ACTIVE")
		terminal.sendLine("/ultra stop")
		terminal.mustSee("Do you really want")
		terminal.sendLine("/ultra stop confirm")
		terminal.mustSee("ULTRA Execution is off")
	}
	terminal.sendLine("/mode auto")
	terminal.mustSee("grants no authority")
	terminal.sendLine("/mode ultra")
	if statepath.PublicEngine {
		terminal.mustSee("ULTRA execution is still OFF")
		terminal.sendLine("/status")
		terminal.mustSee("Runtime mode: ULTRA")
		terminal.sendLine("/ultra status")
		terminal.mustSee("ENTITLED, EXECUTION OFF")
	} else {
		terminal.mustSee("ULTRA is unavailable")
		terminal.sendLine("/status")
		terminal.mustSee("Runtime mode: AUTO")
	}
}
