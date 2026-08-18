package scheduler

import "testing"

func TestCandidateStruct(t *testing.T) {
	c := Candidate{AgentID: "agent-1", Provider: "claude"}
	if c.AgentID != "agent-1" {
		t.Fatalf("expected agent-1, got %s", c.AgentID)
	}
}
