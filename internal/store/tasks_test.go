package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestImportTasksRollsBackWhenDependencyIsMissing(t *testing.T) {
	st := projectStore(t)
	tasks := []model.Task{{
		ID:           "TASK-002",
		Title:        "consumer",
		Status:       model.TaskReady,
		Risk:         model.R1,
		Revision:     0,
		Dependencies: []string{"TASK-404"},
	}}
	if _, err := st.ImportTasks(context.Background(), tasks); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("error = %v, want conflict", err)
	}
	if got := countRows(t, st, "tasks"); got != 0 {
		t.Fatalf("tasks = %d, want rollback to 0", got)
	}
}

func TestImportTasksIsIdempotentAndDivergenceConflicts(t *testing.T) {
	st := projectStore(t)
	tasks := []model.Task{
		{ID: "TASK-001", Title: "base", Status: model.TaskReady, Risk: model.R1, Revision: 0},
		{ID: "TASK-002", Title: "consumer", Status: model.TaskProposed, Risk: model.R1, Revision: 0, Dependencies: []string{"TASK-001"}},
	}
	result, err := st.ImportTasks(context.Background(), tasks)
	if err != nil {
		t.Fatal(err)
	}
	if result.Added != 2 || result.Matched != 0 {
		t.Fatalf("first import = %#v", result)
	}
	result, err = st.ImportTasks(context.Background(), tasks)
	if err != nil {
		t.Fatal(err)
	}
	if result.Added != 0 || result.Matched != 2 {
		t.Fatalf("second import = %#v", result)
	}
	tasks[0].Title = "different"
	if _, err := st.ImportTasks(context.Background(), tasks); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("divergent import error = %v", err)
	}
}

func TestClaimRejectsUnsatisfiedDependency(t *testing.T) {
	st := projectStore(t)
	importTasks(t, st,
		model.Task{ID: "TASK-001", Title: "dependency", Status: model.TaskReady, Risk: model.R1},
		model.Task{ID: "TASK-002", Title: "consumer", Status: model.TaskReady, Risk: model.R1, Dependencies: []string{"TASK-001"}},
	)
	agent, session := activeDeveloper(t, st, "one")
	_, err := st.ClaimTask(context.Background(), model.ClaimRequest{
		TaskID:           "TASK-002",
		AgentID:          agent.ID,
		SessionID:        session.ID,
		ExpectedRevision: 0,
		ExpiresAt:        time.Now().UTC().Add(time.Hour),
	})
	if !errors.Is(err, model.ErrConflict) {
		t.Fatalf("error = %v, want conflict", err)
	}
}

func TestReadyTasksOrdersDependencyUnblockingThenID(t *testing.T) {
	st := projectStore(t)
	importTasks(t, st,
		model.Task{ID: "TASK-A", Title: "unblocks two", Status: model.TaskReady, Risk: model.R1},
		model.Task{ID: "TASK-B", Title: "unblocks one", Status: model.TaskReady, Risk: model.R1},
		model.Task{ID: "TASK-C", Title: "also ready", Status: model.TaskReady, Risk: model.R1},
		model.Task{ID: "TASK-D1", Title: "consumer one", Status: model.TaskProposed, Risk: model.R1, Dependencies: []string{"TASK-A"}},
		model.Task{ID: "TASK-D2", Title: "consumer two", Status: model.TaskProposed, Risk: model.R1, Dependencies: []string{"TASK-A"}},
		model.Task{ID: "TASK-D3", Title: "consumer three", Status: model.TaskProposed, Risk: model.R1, Dependencies: []string{"TASK-B"}},
	)
	got, err := st.ReadyTasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"TASK-A", "TASK-B", "TASK-C"}
	if len(got) != len(want) {
		t.Fatalf("ready count = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].ID != want[i] {
			t.Fatalf("ready[%d] = %s, want %s", i, got[i].ID, want[i])
		}
	}
}

func TestConcurrentClaimHasExactlyOneWinner(t *testing.T) {
	st := projectStore(t)
	importTasks(t, st, model.Task{ID: "TASK-001", Title: "race", Status: model.TaskReady, Risk: model.R1})

	const contenders = 32
	type contender struct {
		agent   model.Agent
		session model.Session
	}
	all := make([]contender, contenders)
	for i := range all {
		all[i].agent, all[i].session = activeDeveloper(t, st, fmt.Sprintf("%02d", i))
	}

	var wins atomic.Int32
	var conflicts atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range all {
		wg.Add(1)
		go func(c contender) {
			defer wg.Done()
			<-start
			_, err := st.ClaimTask(context.Background(), model.ClaimRequest{
				TaskID:           "TASK-001",
				AgentID:          c.agent.ID,
				SessionID:        c.session.ID,
				ExpectedRevision: 0,
				ExpiresAt:        time.Now().UTC().Add(time.Hour),
			})
			switch {
			case err == nil:
				wins.Add(1)
			case errors.Is(err, model.ErrConflict):
				conflicts.Add(1)
			default:
				t.Errorf("claim: %v", err)
			}
		}(all[i])
	}
	close(start)
	wg.Wait()
	if wins.Load() != 1 || conflicts.Load() != contenders-1 {
		t.Fatalf("wins=%d conflicts=%d", wins.Load(), conflicts.Load())
	}
	if got := countWhere(t, st, "leases", "status = 'active'"); got != 1 {
		t.Fatalf("active leases = %d, want 1", got)
	}
}

func TestClaimRejectsStaleRevisionAndReleaseChecksOwner(t *testing.T) {
	st := projectStore(t)
	importTasks(t, st, model.Task{ID: "TASK-001", Title: "revision", Status: model.TaskReady, Risk: model.R1, Revision: 2})
	firstAgent, firstSession := activeDeveloper(t, st, "first")
	secondAgent, secondSession := activeDeveloper(t, st, "second")

	if _, err := st.ClaimTask(context.Background(), model.ClaimRequest{
		TaskID: "TASK-001", AgentID: firstAgent.ID, SessionID: firstSession.ID,
		ExpectedRevision: 0, ExpiresAt: time.Now().UTC().Add(time.Hour),
	}); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("stale claim error = %v", err)
	}
	lease, err := st.ClaimTask(context.Background(), model.ClaimRequest{
		TaskID: "TASK-001", AgentID: firstAgent.ID, SessionID: firstSession.ID,
		ExpectedRevision: 2, ExpiresAt: time.Now().UTC().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ReleaseTask(context.Background(), model.ReleaseRequest{
		TaskID: "TASK-001", LeaseID: lease.ID, SessionID: secondSession.ID, AgentID: secondAgent.ID,
		ExpectedRevision: 3,
	}); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("foreign release error = %v", err)
	}
	if err := st.ReleaseTask(context.Background(), model.ReleaseRequest{
		TaskID: "TASK-001", LeaseID: lease.ID, SessionID: firstSession.ID, AgentID: firstAgent.ID,
		ExpectedRevision: 3,
	}); err != nil {
		t.Fatalf("owner release: %v", err)
	}
}

func TestExpiredLeaseDoesNotAuthorizeTaskSteal(t *testing.T) {
	st := projectStore(t)
	importTasks(t, st, model.Task{ID: "TASK-001", Title: "expired lease", Status: model.TaskReady, Risk: model.R1})
	firstAgent, firstSession := activeDeveloper(t, st, "first-expired")
	secondAgent, secondSession := activeDeveloper(t, st, "second-expired")
	if _, err := st.ClaimTask(context.Background(), model.ClaimRequest{TaskID: "TASK-001", AgentID: firstAgent.ID,
		SessionID: firstSession.ID, ExpectedRevision: 0, ExpiresAt: time.Now().UTC().Add(5 * time.Millisecond)}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	_, err := st.ClaimTask(context.Background(), model.ClaimRequest{TaskID: "TASK-001", AgentID: secondAgent.ID,
		SessionID: secondSession.ID, ExpectedRevision: 1, ExpiresAt: time.Now().UTC().Add(time.Hour)})
	if !errors.Is(err, model.ErrConflict) {
		t.Fatalf("steal error = %v", err)
	}
	if got := countWhere(t, st, "leases", "status = 'active'"); got != 1 {
		t.Fatalf("active leases = %d", got)
	}
}

func projectStore(t *testing.T) *Store {
	t.Helper()
	st := openTestStore(t)
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := st.InitProject(context.Background(), model.Project{
		ID: "PROJECT-local", Repository: "/repo", DefaultBranch: "main", PackVersion: "6.0.0",
	}); err != nil {
		t.Fatal(err)
	}
	return st
}

func activeDeveloper(t *testing.T, st *Store, suffix string) (model.Agent, model.Session) {
	t.Helper()
	agent := model.Agent{
		ID: "AGENT-" + suffix, ProjectID: "PROJECT-local", DisplayName: "developer-" + suffix,
		Role: model.RoleDeveloper, Status: model.AgentRegistered,
	}
	if err := st.RegisterAgent(context.Background(), agent); err != nil {
		t.Fatal(err)
	}
	session, err := st.StartSession(context.Background(), model.SessionStart{
		ID: "SESSION-" + suffix, AgentID: agent.ID, ProjectID: agent.ProjectID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return agent, session
}

func importTasks(t *testing.T, st *Store, tasks ...model.Task) {
	t.Helper()
	if _, err := st.ImportTasks(context.Background(), tasks); err != nil {
		t.Fatal(err)
	}
}

func countRows(t *testing.T, st *Store, table string) int {
	t.Helper()
	return queryInt(t, st.db, "SELECT count(*) FROM "+table)
}

func countWhere(t *testing.T, st *Store, table, where string) int {
	t.Helper()
	return queryInt(t, st.db, "SELECT count(*) FROM "+table+" WHERE "+where)
}
