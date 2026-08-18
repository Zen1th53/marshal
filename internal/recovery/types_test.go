package recovery

import "testing"

func TestPlanStruct(t *testing.T) {
	p := Plan{TaskID: "t-1", CheckpointID: "cp-1", Action: "RESTORE"}
	if p.TaskID != "t-1" {
		t.Fatalf("expected t-1, got %s", p.TaskID)
	}
}
