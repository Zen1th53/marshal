//go:build linux

package tui

import "testing"

// Without a verified entitlement the real binary refuses ULTRA, and a mode
// change says that it grants no authority.
func TestPTYModeIsAPreferenceNotAnAuthority(t *testing.T) {
	bin := buildMarshalBinary(t)
	sweepWorkEnvironment(t)
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	project := initProject(t, bin)
	t.Chdir(project)
	terminal := startFrozenTUIInProject(t, 40, 200, bin, project, "tui", "SESSION-pty-mode")
	terminal.mustSee("MARSHAL")
	terminal.sendLine("/mode auto")
	terminal.mustSee("grants no authority")
	terminal.sendLine("/mode ultra")
	terminal.mustSee("ULTRA is unavailable")
	terminal.sendLine("/status")
	terminal.mustSee("Runtime mode: AUTO")
}
