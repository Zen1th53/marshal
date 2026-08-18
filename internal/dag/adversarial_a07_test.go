package dag

import (
	"testing"
)

func FuzzTaskIDValidationNeverPanics(f *testing.F) {
	for _, seed := range []string{"TASK-A", "", "TASK-\x00", "TASK-../x", "TASK-"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		_ = (Node{TaskID: TaskID(value), Kind: NodeKindTask, Status: StatusPending}).Validate()
		_ = (AddNodeRequest{RequestID: RequestID(value), Node: Node{TaskID: "TASK-A", Kind: NodeKindTask, Status: StatusPending}}).Validate()
	})
}

func TestT29A07ReadinessIsDeterministicAndDoesNotTrustText(t *testing.T) {
	backend := &a04Backend{}
	engine, err := NewEngine(backend)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Ready(t.Context(), "TASK-claimed-by-agent"); err == nil {
		t.Fatal("unknown task text unexpectedly became readiness")
	}
}
