//go:build !governed

package app

import (
	"context"
	"errors"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/projectid"
	"reflect"
	"testing"
	"time"
)

func TestApprovedFallbackProfileRejectsChangedCurrentWitness(t *testing.T) {
	ctx := context.Background()
	fixture, _ := marshalFixture(t, 1)
	runtime := requiredProcess05TestRuntime(t, fixture)
	ctx = requiredTestOwnerContext(t, runtime, ctx)
	projectRecord, err := runtime.Store().Project(ctx)
	if err != nil {
		t.Fatal(err)
	}
	goal := planGoal()
	goal.ID = "GOAL-p05-failover"
	goal.SessionID = "SESSION-p05-failover"
	goal.ProjectID = requiredProcess05TestProjectID(runtime, projectRecord.ID)
	if err = runtime.Store().SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatal(err)
	}
	request := requiredProcess05PlanRequest(planCreateRequest())
	request.SessionID = goal.SessionID
	request.ProjectID = projectid.ID(requiredProcess05TestProjectID(runtime, projectRecord.ID))
	const fixtureVersion = "owned-host-adapter-fixture"
	request.HarnessCandidates[0].Profile.Harness = "codex"
	request.HarnessCandidates[0].Profile.InstalledVersion = fixtureVersion
	request.HarnessCandidates[0].InstalledVersion = fixtureVersion
	request.HarnessCandidates[0].Profile.ExpiresAt = time.Now().UTC().Add(time.Hour)
	fallbackProfile := model.HarnessProfile{Harness: "antigravity", InstalledVersion: fixtureVersion, SupportedModels: []string{"test-agy-model"}, DefaultModel: "test-agy-model", ProbeEvidenceID: "owned-AGY-fallback-probe", ProbedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour)}
	request.HarnessCandidates = append(request.HarnessCandidates, plan.HarnessCandidate{Profile: fallbackProfile, InstalledVersion: fixtureVersion, Provider: "agy", Capacity: goalintake.UnknownCapacity("agy", true)})
	for _, candidate := range request.HarnessCandidates {
		if err := runtime.Store().SaveHarnessProfile(ctx, candidate.Profile); err != nil {
			t.Fatal(err)
		}
	}
	approved, err := runtime.Plans().Create(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	producer, ok := approved.Assignments.For(plan.RoleDeveloper)
	if !ok || producer.Fallback == nil || marshalHarnessName(normalizeProvider(producer.Fallback.Harness)) != "antigravity" || producer.Fallback.Model != "test-agy-model" {
		t.Fatal("fixture lacks actual builder-selected producer fallback")
	}
	approved.Checks = map[string][]string{"fix": {"grep -q fixed README.md"}}
	if err = runtime.Store().SavePlan(ctx, approved, approved.Version); err != nil {
		t.Fatal(err)
	}
	approved, err = runtime.Plans().Approve(ctx, projectid.ID(requiredProcess05TestProjectID(runtime, projectRecord.ID)))
	if err != nil {
		t.Fatal(err)
	}

	s := &MarshalService{}
	s.Store = runtime.Store()
	version := fixtureVersion
	s.InstalledVersion = func(context.Context, string) string { return version }
	authored := approved.Tasks[0]
	run := execution.ExecutionRun{RunID: "profile-read-only", ProjectID: approved.ProjectID, PlanID: approved.ID, PlanVersion: approved.Version, GoalID: approved.Goal.GoalID, GoalRevision: approved.Goal.Revision}
	task := execution.TaskExecution{RunID: run.RunID, TaskID: authored.ID, CanonicalTaskID: canonicalPlanTaskID(approved.ID, approved.Version, authored.ID), Description: authored.Title, TargetFiles: authored.Paths, Dependencies: authored.DependsOn, Mutates: authored.Mutating}
	if authored.ExpectedOutput != "" {
		task.OutputArtifacts = []string{authored.ExpectedOutput}
	}
	for _, o := range approved.Verification.Obligations {
		for _, id := range o.Tasks {
			if id == task.TaskID {
				task.RequiredEvidence = append(task.RequiredEvidence, o.Criterion)
			}
		}
	}
	if err := runtime.validateApprovedFallbackProfile(ctx, s, run, task, "agy"); err != nil {
		t.Fatal("genuine current witness refused", err)
	}
	run.Tasks = map[string]execution.TaskExecution{task.TaskID: task}
	for _, scenario := range []string{"missing-task", "not-requested", "missing-durable-decision"} {
		t.Run(scenario, func(t *testing.T) {
			request := driver.Request{RunID: "NO-durable-decision", Task: marshal.Task{PlanTaskID: task.TaskID, Worker: "agy"}}
			request.Task.ProviderFailoverPending = true
			switch scenario {
			case "missing-task":
				request.Task.PlanTaskID = "missing"
			case "not-requested":
				request.Task.ProviderFailoverPending = false
			}
			before := run
			result, err := runtime.prepareProcess05ProviderFailover(ctx, s, marshal.Run{}, request, &run)
			if scenario == "missing-durable-decision" {
				if err == nil {
					t.Fatal("missing durable decision rebind accepted")
				}
			} else if err != nil || result != &run {
				t.Fatal("non-failover request acquired effects", err)
			}
			if !reflect.DeepEqual(before, run) {
				t.Fatal("non-authorized request changed binding")
			}
		})
	}
	original, err := s.Store.GetHarnessProfile(ctx, "antigravity")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*model.HarnessProfile){
		"expired":         func(p *model.HarnessProfile) { p.ExpiresAt = time.Now().UTC().Add(-time.Minute) },
		"future":          func(p *model.HarnessProfile) { p.ProbedAt = time.Now().UTC().Add(time.Hour) },
		"probe-id":        func(p *model.HarnessProfile) { p.ProbeEvidenceID = "foreign" },
		"version":         func(p *model.HarnessProfile) { p.InstalledVersion = "changed" },
		"model":           func(p *model.HarnessProfile) { p.SupportedModels = []string{"different"}; p.DefaultModel = "different" },
		"refreshed-probe": func(p *model.HarnessProfile) { p.ProbedAt = p.ProbedAt.Add(time.Second) },
		"missing-expiry":  func(p *model.HarnessProfile) { p.ExpiresAt = time.Time{} },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			p := *original
			change(&p)
			if err := s.Store.SaveHarnessProfile(ctx, p); err != nil {
				t.Fatal(err)
			}
			if err := runtime.validateApprovedFallbackProfile(ctx, s, run, task, "agy"); err == nil {
				t.Fatal("changed current profile accepted")
			}
			if err := s.Store.SaveHarnessProfile(ctx, *original); err != nil {
				t.Fatal(err)
			}
		})
	}
	version = "foreign"
	if err := runtime.validateApprovedFallbackProfile(ctx, s, run, task, "agy"); err == nil {
		t.Fatal("installed binary mismatch accepted")
	}
	version = fixtureVersion
	for _, scenario := range []string{"task", "canonical", "output", "plan", "goal", "provider"} {
		t.Run(scenario, func(t *testing.T) {
			r, current, next := run, task, "agy"
			switch scenario {
			case "task":
				current.TaskID = "foreign"
			case "canonical":
				current.CanonicalTaskID = "foreign"
			case "output":
				current.OutputArtifacts = []string{"caller"}
			case "plan":
				r.PlanVersion++
			case "goal":
				r.GoalRevision++
			case "provider":
				next = "claude"
			}
			if err := runtime.validateApprovedFallbackProfile(ctx, s, r, current, next); err == nil {
				t.Fatal("foreign scope accepted")
			}
		})
	}
}

func TestApprovedFallbackRetainsOnlyCurrentApplicableContractScope(t *testing.T) {
	for _, scenario := range []string{"beta-only", "alpha", "purpose", "architecture"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			fixture, _ := marshalFixture(t, 1)
			runtime := requiredProcess05TestRuntime(t, fixture)
			ctx = requiredTestOwnerContext(t, runtime, ctx)
			projectRecord, err := runtime.Store().Project(ctx)
			if err != nil {
				t.Fatal(err)
			}
			goal := planGoal()
			goal.ID = "GOAL-p05-failover"
			goal.SessionID = "SESSION-p05-failover"
			goal.ProjectID = requiredProcess05TestProjectID(runtime, projectRecord.ID)
			if err = runtime.Store().SaveGoalContract(ctx, goal, 0); err != nil {
				t.Fatal(err)
			}
			request := requiredProcess05PlanRequest(planCreateRequest())
			request.SessionID = goal.SessionID
			request.ProjectID = projectid.ID(requiredProcess05TestProjectID(runtime, projectRecord.ID))
			const fixtureVersion = "owned-host-adapter-fixture"
			request.HarnessCandidates[0].Profile.Harness = "codex"
			request.HarnessCandidates[0].Profile.InstalledVersion = fixtureVersion
			request.HarnessCandidates[0].InstalledVersion = fixtureVersion
			request.HarnessCandidates[0].Profile.ExpiresAt = time.Now().UTC().Add(time.Hour)
			fallbackProfile := model.HarnessProfile{Harness: "antigravity", InstalledVersion: fixtureVersion, SupportedModels: []string{"test-agy-model"}, DefaultModel: "test-agy-model", ProbeEvidenceID: "owned-AGY-fallback-probe", ProbedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour)}
			request.HarnessCandidates = append(request.HarnessCandidates, plan.HarnessCandidate{Profile: fallbackProfile, InstalledVersion: fixtureVersion, Provider: "agy", Capacity: goalintake.UnknownCapacity("agy", true)})
			for _, candidate := range request.HarnessCandidates {
				if err := runtime.Store().SaveHarnessProfile(ctx, candidate.Profile); err != nil {
					t.Fatal(err)
				}
			}
			acceptedInitial, err := runtime.store.ReadAcceptedProjectContract(ctx, runtime.ProjectContractBinding())
			if err != nil {
				t.Fatal(err)
			}
			record, err := runtime.store.GetMemoryV2(ctx, runtime.ProjectID(), "MEM-required-resolver")
			if err != nil {
				t.Fatal(err)
			}
			other := record
			other.ID = "MEM-private-beta"
			other.Body = "Private beta policy"
			other.ACLScope = "owner"
			other.ContentDigest = other.CanonicalDigest()
			if err = runtime.store.WriteMemoryV2(ctx, other); err != nil {
				t.Fatal(err)
			}
			initial := acceptedInitial.Contract
			initial.Required = append(append([]projectcontext.MemoryRef(nil), initial.Required...), projectcontext.MemoryRef{ID: other.ID, ProjectID: other.ProjectID, Scope: other.Scope, ScopeID: other.ScopeID, Revision: other.Revision, ContentDigest: other.ContentDigest, StateFingerprint: projectcontext.MemoryFingerprint(other), Class: projectcontext.Policy, Applies: projectcontext.Applicability{TaskIDs: []string{"beta"}}, Reason: "Reviewed beta-only policy", AcceptanceOccurrence: "owner-beta"})
			acceptWatchContract(t, runtime, initial, "fallback-initial-beta-"+scenario)

			approved, err := runtime.Plans().Create(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			producer, ok := approved.Assignments.For(plan.RoleDeveloper)
			if !ok || producer.Fallback == nil || marshalHarnessName(normalizeProvider(producer.Fallback.Harness)) != "antigravity" || producer.Fallback.Model != "test-agy-model" {
				t.Fatal("fixture lacks actual builder-selected producer fallback")
			}
			approved.Checks = map[string][]string{"fix": {"grep -q fixed README.md"}}
			if err = runtime.Store().SavePlan(ctx, approved, approved.Version); err != nil {
				t.Fatal(err)
			}
			approved, err = runtime.Plans().Approve(ctx, projectid.ID(requiredProcess05TestProjectID(runtime, projectRecord.ID)))
			if err != nil {
				t.Fatal(err)
			}

			s := &MarshalService{}
			s.Store = runtime.Store()
			version := fixtureVersion
			s.InstalledVersion = func(context.Context, string) string { return version }
			authored := approved.Tasks[0]
			run := execution.ExecutionRun{RunID: "profile-read-only", ProjectID: approved.ProjectID, PlanID: approved.ID, PlanVersion: approved.Version, GoalID: approved.Goal.GoalID, GoalRevision: approved.Goal.Revision}
			task := execution.TaskExecution{RunID: run.RunID, TaskID: authored.ID, CanonicalTaskID: canonicalPlanTaskID(approved.ID, approved.Version, authored.ID), Description: authored.Title, TargetFiles: authored.Paths, Dependencies: authored.DependsOn, Mutates: authored.Mutating}
			if authored.ExpectedOutput != "" {
				task.OutputArtifacts = []string{authored.ExpectedOutput}
			}
			for _, o := range approved.Verification.Obligations {
				for _, id := range o.Tasks {
					if id == task.TaskID {
						task.RequiredEvidence = append(task.RequiredEvidence, o.Criterion)
					}
				}
			}
			if err := runtime.validateApprovedFallbackProfile(ctx, s, run, task, "agy"); err != nil {
				t.Fatal("genuine current witness refused", err)
			}

			accepted, err := runtime.store.ReadAcceptedProjectContract(ctx, runtime.ProjectContractBinding())
			if err != nil {
				t.Fatal(err)
			}
			next := accepted.Contract
			next.Required = append([]projectcontext.MemoryRef(nil), next.Required...)
			if scenario == "purpose" {
				next.Purpose = "Changed purpose"
			} else if scenario == "architecture" {
				next.Components = append([]projectcontext.Component(nil), next.Components...)
				next.Components[0].Responsibility = "Changed reviewed component responsibility"
			} else {
				id := "MEM-private-beta"
				if scenario == "alpha" {
					id = "MEM-required-resolver"
				}
				record, err := runtime.store.GetMemoryV2(ctx, runtime.ProjectID(), id)
				if err != nil {
					t.Fatal(err)
				}
				record, err = runtime.store.UpdateMemory(ctx, record.ProjectID, record.ID, record.Revision, func(m *model.MemoryRecordV2) error { m.Body = "Changed reviewed policy"; return nil })
				if err != nil {
					t.Fatal(err)
				}
				for i := range next.Required {
					if next.Required[i].ID == id {
						next.Required[i].Revision = record.Revision
						next.Required[i].ContentDigest = record.ContentDigest
						next.Required[i].StateFingerprint = projectcontext.MemoryFingerprint(record)
					}
				}
			}
			acceptWatchContract(t, runtime, next, "fallback-scope-"+scenario)
			if err := runtime.validateApprovedFallbackProfile(ctx, s, run, task, "agy"); scenario == "beta-only" {
				if err != nil {
					t.Fatal("unrelated beta revision blocked owner-reviewed alpha fallback", err)
				}
			} else if !errors.Is(err, projectcontext.ErrChanged) {
				t.Fatal("changed applicable mandatory contract did not return its expected scope-change refusal", err)
			}
		})
	}
}
