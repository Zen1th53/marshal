package store

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestMarshalRetryRetainsSupersededAttemptAtomically(t *testing.T) {
	s := openTestStore(t)
	marshalTestProject(t, s)
	run := marshal.Run{PlanID: "plan", PlanVersion: 1, Settings: marshal.DefaultSettings(), State: marshal.Reviewing, Tasks: []marshal.Task{{PlanTaskID: "a", State: marshal.Returned, BaseCommit: "base", ResultCommit: "failed-head", Branch: "marshal/run/a"}}}
	if err := s.SaveMarshalState(t.Context(), "p", "run", run, 0, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	run.Tasks[0].ResultCommit = ""
	run.Tasks[0].State = marshal.Queued
	if _, err := s.db.Exec(`CREATE TRIGGER reject_attempt_task BEFORE UPDATE ON marshal_tasks BEGIN SELECT RAISE(ABORT,'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveMarshalState(t.Context(), "p", "run", run, 1, nil, nil, 0); err == nil {
		t.Fatal("partial retry save succeeded")
	}
	history, err := s.MarshalFailedAttempts(t.Context(), "p", "run", "a")
	if err != nil || len(history) != 0 {
		t.Fatalf("history escaped rollback: %+v %v", history, err)
	}
	if _, err := s.db.Exec(`DROP TRIGGER reject_attempt_task`); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveMarshalState(t.Context(), "p", "run", run, 1, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	history, err = s.MarshalFailedAttempts(t.Context(), "p", "run", "a")
	if err != nil || len(history) != 1 || history[0].Revision != 1 || history[0].Value.ResultCommit != "failed-head" || history[0].Value.Branch != "marshal/run/a" {
		t.Fatalf("lost failed identity: %+v %v", history, err)
	}
	if err := s.SaveMarshalState(t.Context(), "p", "run", run, 1, nil, nil, 0); err == nil {
		t.Fatal("stale retry succeeded")
	}
	history, err = s.MarshalFailedAttempts(t.Context(), "p", "run", "a")
	if err != nil || len(history) != 1 {
		t.Fatalf("stale retry duplicated history: %+v %v", history, err)
	}
}

func TestMarshalLegacyUpgradePausesWithoutInventingCompatibility(t *testing.T) {
	for _, state := range []marshal.RunState{marshal.Dispatching, marshal.Reviewing, marshal.Verifying, marshal.Merging} {
		t.Run(string(state), func(t *testing.T) {
			s := openTestStore(t)
			marshalTestProject(t, s)
			// A v108 record has no protocol marker. Reopen through the registered
			// migration, retaining its exact run and pending-operation snapshots.
			run := marshal.Run{PlanID: "legacy", PlanVersion: 1, State: state, Settings: marshal.DefaultSettings(), Tasks: []marshal.Task{{PlanTaskID: "a", ResultCommit: "retained-head", State: marshal.Accepted}}, Operation: &marshal.LifecycleOperation{ID: "pending", Kind: "launch", TaskID: "a", Dir: "retained-worktree"}}
			body, err := json.Marshal(run)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`DROP TABLE marshal_run_compatibility; DROP TABLE marshal_attempt_history; DELETE FROM schema_migrations WHERE version=109`); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`INSERT INTO marshal_runs(run_id,project_id,revision,data_json) VALUES('legacy','p',1,?)`, string(body)); err != nil {
				t.Fatal(err)
			}
			if err := s.Migrate(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := s.PauseIncompatibleMarshalRuns(t.Context(), "p"); err != nil {
				t.Fatal(err)
			}
			got, err := s.GetMarshalRun(t.Context(), "p", "legacy")
			if err != nil || got.Value.State != marshal.AwaitingUser || got.Value.Pause == nil || len(got.Value.Pause.Resolutions) == 0 || got.Value.Operation == nil || got.Value.Tasks[0].ResultCommit != "retained-head" {
				t.Fatalf("invisible or destructive recovery: %+v %v", got, err)
			}
			c, err := s.MarshalRunCompatibility(t.Context(), "p", "legacy")
			if err != nil || c.Resumable || c.ProtocolVersion != 0 || c.Reason == "" {
				t.Fatalf("invented legacy compatibility: %+v %v", c, err)
			}
			pending, err := s.MarshalUnfinishedRuns(t.Context(), "p")
			if err != nil || len(pending) != 0 {
				t.Fatalf("legacy operation admitted for replay: %v %v", pending, err)
			}
			if err := s.PauseIncompatibleMarshalRuns(t.Context(), "p"); err != nil {
				t.Fatal(err)
			}
			again, _ := s.GetMarshalRun(t.Context(), "p", "legacy")
			if again.Revision != got.Revision {
				t.Fatal("reopen rewrote same pause")
			}
			got.Value.State = state
			if err := s.SaveMarshalState(t.Context(), "p", "legacy", got.Value, got.Revision, nil, nil, 0); !errors.Is(err, model.ErrConflict) {
				t.Fatalf("legacy resumed without review: %v", err)
			}
			if rows := queryInt(t, s.db, `SELECT count(*) FROM pragma_foreign_key_check`); rows != 0 {
				t.Fatal("migration broke foreign keys")
			}
		})
	}
}

func TestMarshalCurrentProtocolSurvivesBuildUpgrade(t *testing.T) {
	s := openTestStore(t)
	marshalTestProject(t, s)
	run := marshal.Run{PlanID: "current", PlanVersion: 1, State: marshal.Dispatching, Settings: marshal.DefaultSettings()}
	if _, err := s.SetMarshalRun(t.Context(), "p", "run", run, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE marshal_run_compatibility SET runtime_build='previous release'`); err != nil {
		t.Fatal(err)
	}
	if err := s.PauseIncompatibleMarshalRuns(t.Context(), "p"); err != nil {
		t.Fatal(err)
	}
	c, err := s.MarshalRunCompatibility(t.Context(), "p", "run")
	if err != nil || !c.Resumable || c.RuntimeBuild != "previous release" {
		t.Fatalf("compatible build upgrade blocked: %+v %v", c, err)
	}
	got, _ := s.GetMarshalRun(t.Context(), "p", "run")
	if got.Revision != 1 || got.Value.State != marshal.Dispatching {
		t.Fatal("compatible run mutated")
	}
}
