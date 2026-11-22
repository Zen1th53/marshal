//go:build !governed

package tui

// planReadyActivity is the Activity line for an imported plan. The tasks
// start only once the Marshal's approval applies; without one the line says
// the approval comes next.
func planReadyActivity(tasks int, approved bool) string {
	if !approved {
		return "A plan with " + count(tasks, "task", "tasks") + " is in; the Marshal was asked to approve it, and the tasks start when it does."
	}
	return "A plan with " + count(tasks, "task", "tasks") + " is ready; the Marshal approved it and the tasks start now."
}
