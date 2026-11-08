//go:build !governed

package store

import (
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/projectid"
)

func TestProjectContextAuthoritySeparatesEngineAdmissionFromCanonicalAttempt(t *testing.T) {
	s, a, request := authorityFixture(t)
	// A transport preflight and resumed provider invocation are two engine
	// admissions within the same canonical task attempt. Neither is a model
	// call count. Canonical task state comes from the real imported store row.
	a.Binding.Attempt = 2
	request.Binding = a.Binding
	repacketContextAdmission(t, s, &a)
	task, err := s.GetTask(t.Context(), a.Binding.CanonicalTaskID)
	if err != nil || task.Attempt != 1 {
		t.Fatalf("expected real initial canonical attempt 1: %+v %v", task, err)
	}
	observation, err := s.ReadProjectContextAuthority(t.Context(), request, a.Now)
	if err != nil {
		t.Fatalf("engine admission 2 incorrectly equated to canonical attempt 1: %v", err)
	}
	if observation.Request.Binding.Attempt != 2 {
		t.Fatal("authority observation silently rewrote the engine counter")
	}
	a.ExpectedAuthority = &observation
	if _, err = s.CommitProjectContextAdmission(t.Context(), a); err != nil {
		t.Fatalf("exact independent counters did not admit: %v", err)
	}
	for _, invalid := range []int{0, -1} {
		request.Binding.Attempt = invalid
		if _, err = s.ReadProjectContextAuthority(t.Context(), request, a.Now); !errors.Is(err, projectcontext.ErrInvalid) {
			t.Fatalf("invalid engine admission counter %d was accepted: %v", invalid, err)
		}
	}
}

func TestProjectContextAuthorityCanonicalRetryStillInvalidatesPriorObservation(t *testing.T) {
	s, a, request := authorityFixture(t)
	// The generic authority-read fixture intentionally has no confirmation.
	// A typed retry requires a confirmed goal and matching fresh plan/task;
	// create those through canonical APIs, preserving the initial rows.
	goal, err := s.GetActiveGoalContract(t.Context(), a.Binding.Session)
	if err != nil {
		t.Fatal(err)
	}
	previousRevision := goal.Revision
	goal.Revision++
	goal.Confirmation = model.ConfirmationApproved
	goal.OriginalRequest = "Fixture owner approves retry currentness verification"
	if err = s.SaveGoalContract(t.Context(), goal, previousRevision); err != nil {
		t.Fatal(err)
	}
	p, err := plan.Build(plan.BuildRequest{Goal: goal, ProjectID: projectid.ID(a.Binding.Project.CanonicalID), Tasks: []plan.Task{{ID: a.Binding.TaskID, Title: "add the cache", Mutating: true, Weight: 1, Paths: []string{"internal/store"}, Criteria: goal.SuccessCriteria}}, Scope: goal.Scope, Version: constitution.Current, Now: a.Now, Candidates: []goalintake.Candidate{{Provider: "codex", Model: a.Binding.Model, Governance: constitution.GovernanceVerified, Capacity: goalintake.UnknownCapacity("codex", true)}}})
	if err != nil || p.State != plan.StateReady {
		t.Fatal("fresh fixture plan not ready", p.BlockedBy, err)
	}
	if err = s.SavePlan(t.Context(), p, 0); err != nil {
		t.Fatal(err)
	}
	p, err = p.Approve(a.Now)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SavePlan(t.Context(), p, p.Version); err != nil {
		t.Fatal(err)
	}
	p, err = s.GetActivePlan(t.Context(), projectid.ID(a.Binding.Project.CanonicalID))
	if err != nil || p.State != plan.StateApproved {
		t.Fatal("fresh fixture plan not approved", err)
	}
	a.Binding.GoalRevision, a.Binding.PlanID, a.Binding.PlanVersion, a.Binding.ApprovalScopeDigest = goal.Revision, p.ID, p.Version, p.ApprovalScopeDigest
	a.Binding.CanonicalTaskID = "TASK-authority-retry"
	a.Selection.CanonicalTaskID = a.Binding.CanonicalTaskID
	importCtx := WithTaskBinding(t.Context(), TaskBinding{a.Binding.Session, a.Binding.GoalID, p.ID, goal.Revision, p.Version})
	if _, err = s.ImportTasks(importCtx, []model.Task{{ID: a.Binding.CanonicalTaskID, Title: "add the cache", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	a.Binding.Attempt = 2
	request.Binding = a.Binding
	repacketContextAdmission(t, s, &a)
	before, err := s.ReadProjectContextAuthority(t.Context(), request, a.Now)
	if err != nil {
		t.Fatal(err)
	}
	a.ExpectedAuthority = &before
	task, err := s.GetTask(t.Context(), a.Binding.CanonicalTaskID)
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := s.TransitionTask(t.Context(), model.TaskTransitionRequest{TaskID: task.ID, FromStatus: task.Status, ToStatus: model.TaskBlocked, ExpectedRevision: task.Revision, ActorID: a.Binding.Principal, ActorRole: model.RoleDeveloper, Reason: "Fixture reports a failed attempt"})
	if err != nil {
		t.Fatal(err)
	}
	// Exercise the existing typed retry transaction instead of directly
	// rewriting task_controls or treating engine admission as task retry.
	ctx := WithCommand(t.Context(), CommandRecord{ProjectID: a.Binding.Project.CanonicalID, Actor: a.Binding.Principal, Key: "context-canonical-retry", Operation: "task.retry", SessionID: a.Binding.Session, TargetID: task.ID, ExpectedVersion: blocked.Revision, Digest: "fixture-canonical-retry"})
	retried, err := s.ApplyTaskCommand(ctx, TaskMutation{Operation: "retry"})
	if err != nil || retried.Attempt != 2 {
		t.Fatalf("typed canonical retry did not advance its own counter: %+v %v", retried, err)
	}
	after, err := s.ReadProjectContextAuthority(t.Context(), request, a.Now)
	if err != nil {
		t.Fatal(err)
	}
	if after.Digest == before.Digest || after.Request.Binding.Attempt != 2 {
		t.Fatal("canonical retry disappeared from current authority, or engine counter changed")
	}
	if _, err = s.CommitProjectContextAdmission(t.Context(), a); !errors.Is(err, projectcontext.ErrChanged) {
		t.Fatalf("stale authority survived actual typed canonical retry: %v", err)
	}
}
