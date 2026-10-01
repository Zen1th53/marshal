//go:build linux

package tui

import "testing"

// The real binary sets budget limits by revising the goal, refuses limits it
// cannot enforce, and shows the limits next to consumption.
func TestPTYBudgetLimitsReviseTheGoal(t *testing.T) {
	bin := buildMarshalBinary(t)
	sweepWorkEnvironment(t)
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	project := initProject(t, bin)
	t.Chdir(project)
	terminal := startFrozenTUIInProject(t, 40, 200, bin, project, "tui", "SESSION-pty-budget")
	terminal.mustSee("MARSHAL")
	terminal.sendLine("/goal create add a remove command to the todo CLI")
	terminal.mustSee("[rev 1]")
	terminal.sendLine("/budget set tokens=1000")
	terminal.mustSee("token and cost limits are not enforced")
	terminal.sendLine("/budget set calls=3 duration=30m")
	terminal.mustSee("revised to revision 2 with the new budget limits")
	terminal.sendLine("/budget")
	terminal.mustSee("Model calls: 3")
	terminal.mustSee("Duration:    30m0s")
}
