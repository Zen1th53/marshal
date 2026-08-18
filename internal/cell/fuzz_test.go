package cell

import "testing"

func FuzzSpecValidationNeverPanics(f *testing.F) {
	f.Add("TASK-cell-fuzz", "/tmp/work", "native")
	f.Add("", "../escape", "unknown")
	f.Fuzz(func(t *testing.T, taskID, workspace, backend string) {
		_ = (Spec{TaskID: TaskID(taskID), Workspace: workspace, Backend: BackendKind(backend)}).Validate()
	})
}
