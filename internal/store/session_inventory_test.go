package store

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestSessionInventoryRunsProjectAndKindBoundaries(t *testing.T) {
	st := projectStore(t)
	ctx := context.Background()
	importTasks(t, st, model.Task{ID: "TASK-inventory", Title: "fixture", Status: model.TaskReady, Risk: model.R1})
	_, session := activeDeveloper(t, st, "inventory")
	run := model.WorkerRun{ID: "RUN-inventory", TaskID: "TASK-inventory", SessionID: session.ID, Adapter: "local", AdapterVersion: "fixture", BaseCommit: "fixture", Status: "running", StartedAt: time.Now().UTC()}
	if err := st.StartRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	exit := 7
	if err := st.FinishRun(ctx, model.RunFinish{ID: run.ID, Status: "failed", ExitStatus: &exit, EndedAt: run.StartedAt.Add(time.Second), ExpectedRevision: 0}); err != nil {
		t.Fatal(err)
	}
	runs, err := st.SessionInventoryRuns(ctx, "PROJECT-local", "")
	if err != nil || len(runs) != 1 || runs[0].ID != run.ID || runs[0].Status != "failed" || runs[0].BaseCommit != run.BaseCommit || runs[0].EndedAt == nil {
		t.Fatalf("finished governed inventory: %+v %v", runs, err)
	}
	for _, tc := range []struct{ project, provider string }{{"PROJECT-foreign", ""}, {"PROJECT-local", "codex"}} {
		runs, err := st.SessionInventoryRuns(ctx, tc.project, tc.provider)
		if err != nil || len(runs) != 0 {
			t.Fatalf("scope %+v: %+v %v", tc, runs, err)
		}
	}
	for _, id := range []string{run.ID, run.TaskID, run.SessionID} {
		known, err := st.IsGovernedSessionID(ctx, id)
		if err != nil || !known {
			t.Fatalf("governed %s: %t %v", id, known, err)
		}
	}
	known, err := st.IsGovernedSessionID(ctx, "unknown")
	if err != nil || known {
		t.Fatalf("unknown kind: %t %v", known, err)
	}
}
