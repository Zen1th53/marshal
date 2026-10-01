//go:build linux

package tui

import "testing"

// The real binary refuses an unknown harness, reports a preference it did not
// use, and never names a model the router invented.
func TestPTYRouteIsHonestAboutHarnessAndModel(t *testing.T) {
	bin := buildMarshalBinary(t)
	sweepWorkEnvironment(t)
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	project := initProject(t, bin)
	t.Chdir(project)
	terminal := startFrozenTUIInProject(t, 40, 200, bin, project, "tui", "SESSION-pty-route")
	terminal.mustSee("MARSHAL")
	terminal.sendLine("/route harness=cursor")
	terminal.mustSee("unknown harness")
	terminal.sendLine("/route role=architect harness=opencode")
	terminal.mustSee("is not used for role architect")
	terminal.mustSee("NOT INSTALLED")
	terminal.mustSee("adapter default, resolved at dispatch")
}
