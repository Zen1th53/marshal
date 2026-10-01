//go:build linux

package tui

import "testing"

// The real binary resolves an API provider to its harness and says that
// /provider config inspects rather than configures.
func TestPTYProviderConfigNamesTheHarness(t *testing.T) {
	bin := buildMarshalBinary(t)
	sweepWorkEnvironment(t)
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	project := initProject(t, bin)
	t.Chdir(project)
	terminal := startFrozenTUIInProject(t, 40, 200, bin, project, "tui", "SESSION-pty-provider")
	terminal.mustSee("MARSHAL")
	terminal.sendLine("/provider config anthropic")
	terminal.mustSee("reached through the claude harness")
	terminal.mustSee("changes no configuration")
}
