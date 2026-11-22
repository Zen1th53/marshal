//go:build governed

package tui

// planReadyActivity is the Activity line for an imported plan. The approval
// popup shows once the Marshal proposes the approval.
func planReadyActivity(tasks int, approved bool) string {
	if !approved {
		return "A plan with " + count(tasks, "task", "tasks") + " is ready; the Marshal was asked to propose its approval, then review it in the popup."
	}
	return "A plan with " + count(tasks, "task", "tasks") + " is ready. Review it and approve it in the popup."
}
