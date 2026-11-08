//go:build !governed

package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
)

func TestExtremeModelPlanInputsPersistChecksBeforeApproval(t *testing.T) {
	r := runtimeForPlan(t)
	req := planCreateRequest()
	req.Mode = plan.ModeUltra
	req.Checks = map[string][]string{"fix": {"test -f README.md"}}
	created, err := r.Plans().Create(t.Context(), req)
	if err != nil || created.State != plan.StateReady || len(created.Checks["fix"]) != 1 {
		t.Fatalf("created: %+v %v", created, err)
	}
	req.Checks["fix"][0] = "false"
	stored, err := r.Plans().Current(t.Context(), req.ProjectID)
	if err != nil || stored.Checks["fix"][0] != "test -f README.md" {
		t.Fatalf("caller changed canonical checks: %+v %v", stored, err)
	}
	approved, err := r.Plans().Approve(t.Context(), req.ProjectID)
	if err != nil || approved.ApprovedScope == nil || approved.ApprovalScopeDigest == "" {
		t.Fatalf("approve: %+v %v", approved, err)
	}
}

func TestExtremeModelPlanInputsRejectMissingUnknownOrEmptyChecks(t *testing.T) {
	for name, checks := range map[string]map[string][]string{"unknown": {"other": {"true"}}, "empty": {"fix": {" "}}, "missing": {"fix": {"true"}}} {
		t.Run(name, func(t *testing.T) {
			r := runtimeForPlan(t)
			req := planCreateRequest()
			req.Checks = checks
			if name == "missing" {
				extra := req.Tasks[0]
				extra.ID = "extra"
				req.Tasks = append(req.Tasks, extra)
			}
			if _, err := r.Plans().Create(t.Context(), req); err == nil {
				t.Fatal("invalid source checks accepted")
			}
			if _, err := r.Plans().Current(t.Context(), req.ProjectID); err == nil {
				t.Fatal("refused input persisted a plan")
			}
		})
	}
}

func TestExtremeModelPlanInputsRejectAmbiguousCriterionMapping(t *testing.T) {
	r := runtimeForPlan(t)
	req := planCreateRequest()
	req.Tasks[0].Criteria = append(req.Tasks[0].Criteria, "another independent criterion")
	req.Checks = map[string][]string{"fix": {"true"}}
	if _, err := r.Plans().Create(t.Context(), req); err == nil {
		t.Fatal("checks ambiguously credited to multiple criteria")
	}
	if _, err := r.Plans().Current(t.Context(), req.ProjectID); err == nil {
		t.Fatal("refused criteria mapping persisted a plan")
	}
}

func TestExtremeModelPlanInputsUseExactCurrentTaskHarness(t *testing.T) {
	r := runtimeForPlan(t)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\nprintf 'codex-cli fixture-1\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	profile := model.HarnessProfile{Harness: "codex", InstalledVersion: "codex-cli fixture-1", SupportedModels: []string{"fixture-model"}, DefaultModel: "fixture-model", ProbeEvidenceID: "FIXTURE-governance-only", ProbedAt: time.Now().UTC()}
	if err := r.Store().SaveHarnessProfile(t.Context(), profile); err != nil {
		t.Fatal(err)
	}
	req := planCreateRequest()
	req.Checks = map[string][]string{"fix": {"true"}}
	req.TaskProviders = map[string]string{"fix": "codex"}
	created, err := r.Plans().Create(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := marshalPlanTaskWorker(created, created.Tasks[0]); err != nil || got != "codex" {
		t.Fatalf("task provider=%s %v", got, err)
	}
	if created.Routes["fix"].Model != "fixture-model" {
		t.Fatal("task model invented or lost")
	}
	for _, assignment := range created.Assignments.Assignments {
		if assignment.Independent && len(assignment.Tasks) == 0 {
			t.Fatal("explicit producer lost independent reviewer task scopes")
		}
	}
	profile.InstalledVersion = "different-installed-version"
	if err := r.Store().SaveHarnessProfile(t.Context(), profile); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Plans().Create(t.Context(), req); err == nil {
		t.Fatal("stale installed governance admitted")
	}
	req.TaskProviders = map[string]string{"fix": "agy"}
	if _, err := r.Plans().Create(t.Context(), req); err == nil {
		t.Fatal("unsupported governed harness admitted")
	}
}

func TestExtremeModelPlanProducerExcludesIndependentCheckingRole(t *testing.T) {
	p := plan.ExecutionPlan{Assignments: plan.AssignmentPlan{Assignments: []plan.Assignment{{Role: plan.RoleQA, Tasks: []string{"fix"}, Harness: "claude", Independent: true}, {Role: plan.RoleDeveloper, Tasks: []string{"fix"}, Harness: "codex"}}}}
	if got, err := marshalPlanTaskWorker(p, plan.Task{ID: "fix"}); err != nil || got != "codex" {
		t.Fatalf("independent reviewer selected as producer: %s %v", got, err)
	}
}

func TestPrivatePlanTaskWorkerNormalizesCanonicalClaudeHarness(t *testing.T) {
	task := plan.Task{ID: "claude-marker", Role: string(plan.RoleDeveloper)}
	p := plan.ExecutionPlan{Assignments: plan.AssignmentPlan{Assignments: []plan.Assignment{{Role: plan.RoleDeveloper, Tasks: []string{task.ID}, Harness: "claude-code", Provider: "claude"}}}}
	if got, handled, err := privatePlanTaskWorker(p, task); err != nil || !handled || got != "claude" {
		t.Fatalf("canonical profile cannot dispatch governed Claude: %q %v %v", got, handled, err)
	}
	p.Assignments.Assignments = append(p.Assignments.Assignments, plan.Assignment{Role: plan.RoleDeveloper, Tasks: []string{task.ID}, Harness: "claude", Provider: "claude"})
	if got, _, err := privatePlanTaskWorker(p, task); err != nil || got != "claude" {
		t.Fatalf("equivalent aliases became ambiguous: %q %v", got, err)
	}
	p.Assignments.Assignments[1].Harness = "codex"
	if _, _, err := privatePlanTaskWorker(p, task); err == nil {
		t.Fatal("distinct governed harnesses admitted as equivalent")
	}
}
