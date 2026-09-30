package store

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

func marshalTestProject(t *testing.T, s *Store) {
	t.Helper()
	if err := s.InitProject(context.Background(), model.Project{ID: "p", Repository: "example/repo", DefaultBranch: "main", PackVersion: "v1"}); err != nil {
		t.Fatal(err)
	}
}

func TestMarshalMigrationFreshAndCurrentHead(t *testing.T) {
	for _, fresh := range []bool{true, false} {
		t.Run(map[bool]string{true: "fresh", false: "current"}[fresh], func(t *testing.T) {
			var s *Store
			if fresh {
				s = openEmptyTestStore(t)
			} else {
				s = openTestStore(t)
			}
			if err := s.Migrate(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := s.Migrate(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := queryInt(t, s.db, "SELECT MAX(version) FROM schema_migrations"); got != LatestSchemaVersion {
				t.Fatalf("version %d", got)
			}
			for _, table := range []string{"marshal_runs", "marshal_tasks", "marshal_handins", "marshal_reviews", "marshal_settings"} {
				if got := queryInt(t, s.db, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table); got != 1 {
					t.Fatalf("missing %s", table)
				}
			}
		})
	}
}

func TestMarshalStaleRevisionLeavesStateUnchanged(t *testing.T) {
	s := openTestStore(t)
	marshalTestProject(t, s)
	ctx := context.Background()
	first := marshal.DefaultSettings()
	if _, err := s.SetMarshalSettings(ctx, "p", first, 0); err != nil {
		t.Fatal(err)
	}
	changed := first
	changed.ReworkLimit = 8
	if _, err := s.SetMarshalSettings(ctx, "p", changed, 0); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("stale write: %v", err)
	}
	got, err := s.GetMarshalSettings(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != 1 || got.Value != first {
		t.Fatalf("mutated: %+v", got)
	}
}

func TestMarshalRunTaskHandInReviewRoundTrip(t *testing.T) {
	s := openTestStore(t)
	marshalTestProject(t, s)
	ctx := context.Background()
	task := marshal.Task{PlanTaskID: "t", Worker: "codex", Mode: marshal.Native, State: marshal.HandedIn, ReturnsByAgent: map[string]int{"codex": 1}, Checks: []marshal.Check{{Command: "go test ./...", Criteria: []string{"passes"}}}, Files: []string{"main.go"}}
	run := marshal.Run{PlanID: "plan", PlanVersion: 1, ApprovalScopeDigest: "digest", BaseCommit: "base", Tier: marshal.Standard, Settings: marshal.DefaultSettings(), GoalBinding: "goal", Tasks: []marshal.Task{task}, State: marshal.Reviewing, CloseAuthorization: &marshal.CloseAuthorization{User: "user", ApprovalScopeDigest: "digest"}}
	handin := marshal.HandIn{BaseCommit: "base", ResultCommit: "head", Diff: "diff", FilesTouched: []string{"main.go"}, Worker: "codex", Mode: marshal.Native, RuntimeObserved: []marshal.CommandRecord{{Command: "go test ./...", ExitCode: 0, Output: "ok"}}, CheckResults: []marshal.CheckResult{{Command: "go test ./...", Criteria: []string{"passes"}, Passed: true, ResultCommit: "head"}}}
	review := marshal.Review{Verdict: marshal.VerdictAccept, Reviewer: "marshal", Reasons: []string{"passed"}, EvidenceRefs: []string{"check"}}
	if _, err := s.SetMarshalRun(ctx, "p", "r", run, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetMarshalTask(ctx, "r", task, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetMarshalHandIn(ctx, "r", "t", 1, handin); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetMarshalReview(ctx, "r", "t", 1, review); err != nil {
		t.Fatal(err)
	}
	gotRun, err := s.GetMarshalRun(ctx, "p", "r")
	if err != nil || !reflect.DeepEqual(gotRun.Value, run) {
		t.Fatalf("run: %+v %v", gotRun, err)
	}
	gotTask, err := s.GetMarshalTask(ctx, "r", "t")
	if err != nil || !reflect.DeepEqual(gotTask.Value, task) {
		t.Fatalf("task: %+v %v", gotTask, err)
	}
	gotHandIn, err := s.GetMarshalHandIn(ctx, "r", "t", 1)
	if err != nil || !reflect.DeepEqual(gotHandIn.Value, handin) {
		t.Fatalf("hand-in: %+v %v", gotHandIn, err)
	}
	gotReview, err := s.GetMarshalReview(ctx, "r", "t", 1)
	if err != nil || !reflect.DeepEqual(gotReview.Value, review) {
		t.Fatalf("review: %+v %v", gotReview, err)
	}
}

func TestMarshalSettingsDefaultsAndInvalidEnum(t *testing.T) {
	s := openTestStore(t)
	marshalTestProject(t, s)
	ctx := context.Background()
	got, err := s.GetMarshalSettings(ctx, "p")
	if err != nil || got.Revision != 0 || got.Value != marshal.DefaultSettings() {
		t.Fatalf("defaults: %+v %v", got, err)
	}
	invalid := marshal.DefaultSettings()
	invalid.ExecutionRights = "invalid"
	if _, err := s.SetMarshalSettings(ctx, "p", invalid, 0); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("invalid enum: %v", err)
	}
	if got := queryInt(t, s.db, "SELECT COUNT(*) FROM marshal_settings"); got != 0 {
		t.Fatalf("wrote %d invalid settings", got)
	}
}

func TestMarshalHandInAndReviewAppendOnly(t *testing.T) {
	s := openTestStore(t)
	marshalTestProject(t, s)
	ctx := context.Background()
	run := marshal.Run{PlanID: "plan", PlanVersion: 1, Settings: marshal.DefaultSettings()}
	if _, err := s.SetMarshalRun(ctx, "p", "r", run, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetMarshalTask(ctx, "r", marshal.Task{PlanTaskID: "t"}, 0); err != nil {
		t.Fatal(err)
	}
	firstHandIn := marshal.HandIn{ResultCommit: "first"}
	secondHandIn := marshal.HandIn{ResultCommit: "second"}
	firstReview := marshal.Review{Reviewer: "first"}
	secondReview := marshal.Review{Reviewer: "second"}
	if _, err := s.SetMarshalHandIn(ctx, "r", "t", 1, firstHandIn); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetMarshalReview(ctx, "r", "t", 1, firstReview); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetMarshalHandIn(ctx, "r", "t", 1, secondHandIn); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("duplicate hand-in: %v", err)
	}
	if _, err := s.SetMarshalReview(ctx, "r", "t", 1, secondReview); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("duplicate review: %v", err)
	}
	gotHandIn, err := s.GetMarshalHandIn(ctx, "r", "t", 1)
	if err != nil || gotHandIn.Revision != 1 || !reflect.DeepEqual(gotHandIn.Value, firstHandIn) {
		t.Fatalf("first hand-in changed: %+v %v", gotHandIn, err)
	}
	gotReview, err := s.GetMarshalReview(ctx, "r", "t", 1)
	if err != nil || gotReview.Revision != 1 || !reflect.DeepEqual(gotReview.Value, firstReview) {
		t.Fatalf("first review changed: %+v %v", gotReview, err)
	}
	if _, err := s.SetMarshalHandIn(ctx, "r", "t", 2, secondHandIn); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetMarshalReview(ctx, "r", "t", 2, secondReview); err != nil {
		t.Fatal(err)
	}
	gotHandIn, err = s.GetMarshalHandIn(ctx, "r", "t", 2)
	if err != nil || gotHandIn.Revision != 1 || !reflect.DeepEqual(gotHandIn.Value, secondHandIn) {
		t.Fatalf("second hand-in: %+v %v", gotHandIn, err)
	}
	gotReview, err = s.GetMarshalReview(ctx, "r", "t", 2)
	if err != nil || gotReview.Revision != 1 || !reflect.DeepEqual(gotReview.Value, secondReview) {
		t.Fatalf("second review: %+v %v", gotReview, err)
	}
}
