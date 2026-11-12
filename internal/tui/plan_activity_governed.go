//go:build governed

package tui

func planReadyActivity(tasks int) string {
	return "A plan with " + count(tasks, "task", "tasks") + " is ready. Review it and approve it in the popup."
}
