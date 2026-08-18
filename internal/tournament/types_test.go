package tournament

import "testing"

func TestCandidateRunStruct(t *testing.T) {
	c := CandidateRun{ID: "cand-1", TaskID: "t-1", AgentID: "agent-a"}
	if c.ID != "cand-1" {
		t.Fatalf("expected cand-1, got %s", c.ID)
	}
}
