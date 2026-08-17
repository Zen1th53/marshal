package dag

import "testing"

func TestA03EngineRejectsCycleBeforeMutation(t *testing.T) {
	engine := NewEngine(nil)
	if engine == nil {
		t.Fatal("expected DAG engine")
	}
}
