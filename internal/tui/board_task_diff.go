package tui

type taskDiffOverlay struct{}

func (o *taskDiffOverlay) HandleKey(w *Workspace, ev KeyEvent) bool { return false }
func (o *taskDiffOverlay) StatusLine(width int) string              { return "" }
func (w *Workspace) openTaskDiff(taskID string) bool                { return false }
