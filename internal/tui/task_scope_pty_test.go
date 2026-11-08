//go:build linux

package tui

import "testing"

// The real binary labels the task list's scope and refuses an active scope
// it cannot establish instead of showing project-wide work under it.
func TestPTYTaskScopeLabels(t *testing.T) {
	bin := buildMarshalBinary(t)
	sweepWorkEnvironment(t)
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	project := initProject(t, bin)
	t.Chdir(project)
	terminal := startFrozenTUIInProject(t, 40, 180, bin, project, "tui", "SESSION-pty-task-scope")
	terminal.mustSee("Marshal")
	terminal.sendLine("/tasks --scope active")
	terminal.mustSee("the active scope is unavailable")
	terminal.sendLine("/task create scoped work")
	// The input line is echoed before the asynchronous command finishes. Wait
	// for the persisted task acknowledgement before sending the next command.
	terminal.mustSee("ready (revision 1, attempt 1): scoped work")
	terminal.sendLine("/tasks")
	terminal.mustSee("scope: project, all tasks in this project")
}
