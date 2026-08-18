package scheduler

import (
	"context"
	"testing"
)

func BenchmarkSchedulerNext(b *testing.B) {
	sched := NewScheduler()
	ctx := context.Background()
	task := Task{TaskID: "t-bm"}
	cands := []Candidate{{AgentID: "a-1"}, {AgentID: "a-2"}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = sched.Next(ctx, task, cands)
	}
}
