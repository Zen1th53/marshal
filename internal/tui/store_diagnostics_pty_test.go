//go:build linux

package tui

import "testing"

func TestPTYStoreDiagnostics(t *testing.T) {
	bin := buildMarshalBinary(t)
	sweepWorkEnvironment(t)
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	project := initProject(t, bin)
	terminal := startFrozenTUIInProject(t, 40, 180, bin, project, "tui", "SESSION-store-diagnostics")
	terminal.mustSee("MARSHAL")
	terminal.sendLine("/store")
	terminal.mustSee("SQLite quick_check passed")
	terminal.mustSee("Inventory counts (not proof of health)")
	terminal.sendLine("/store check full")
	terminal.mustSee("Full integrity_check can take long")
	terminal.mustSee("SQLite integrity_check passed")
}
