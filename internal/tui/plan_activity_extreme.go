//go:build !governed

package tui

func planReadyActivity(tasks int) string {
	return "A plan with " + count(tasks, "task", "tasks") + " is ready; the Marshal approved it and the tasks start now."
}
