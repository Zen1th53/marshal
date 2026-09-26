//go:build linux

package tui

import (
	"context"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
)

// The Process 05 entry point must be reachable through the shipped terminal
// binary and refuse execution until a canonical approved plan exists.
func TestPTYMarshalUsePlanRequiresApprovedPlan(t *testing.T) {
	s := startCommandTUI(t, 40, 160)
	s.sendLine("/marshal use-plan")
	s.mustSee("execution plan not found")
}

func TestPTYMarshalUsePlanReachesProcess05Approval(t *testing.T) {
	bin := buildMarshalBinary(t)
	project := initProject(t, bin)
	ctx := context.Background()
	runtime, err := app.Open(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	binding, found := projectid.LoadBinding(filepath.Join(project, projectid.StateDirName))
	if !found {
		t.Fatal("initialized project has no durable identity")
	}
	projectID := binding.ID
	versionOutput, err := exec.Command("codex", "--version").Output()
	if err != nil {
		t.Skipf("Codex CLI unavailable: %v", err)
	}
	version := strings.TrimSpace(strings.SplitN(string(versionOutput), "\n", 2)[0])
	profile := model.HarnessProfile{Harness: "codex", InstalledVersion: version, SupportedModels: []string{"gpt-test"}, DefaultModel: "gpt-test", ProbeEvidenceID: "pty-process05-probe", ProbedAt: time.Now().UTC()}
	if err := runtime.Store().SaveHarnessProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	goal := model.GoalContract{ID: "GOAL-pty-process05", SessionID: "SESSION-pty-process05", ProjectID: string(projectID), Revision: 1,
		OriginalRequest: "Fix README.md", Confirmation: model.ConfirmationApproved, ConstitutionVersion: constitution.Current.String(),
		DesiredOutcome: "README.md contains fixed", ExpectedArtifact: "README.md", SuccessCriteria: []string{"README.md contains fixed"},
		Risk: model.R1, AuthoritySource: "operator"}
	if err := runtime.Store().SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatal(err)
	}
	capacity := goalintake.UnknownCapacity("codex", true)
	p, err := runtime.Plans().Create(ctx, app.CreatePlanRequest{SessionID: goal.SessionID, ProjectID: projectID,
		Tasks:             []plan.Task{{ID: "fix", Title: "fix README.md", Mutating: true, Weight: 1, Paths: []string{"README.md"}, Criteria: goal.SuccessCriteria}},
		Candidates:        []goalintake.Candidate{{Provider: "codex", Model: "gpt-test", Capacity: capacity, Governance: constitution.GovernanceVerified}},
		HarnessCandidates: []plan.HarnessCandidate{{Profile: profile, InstalledVersion: version, Provider: "codex", Capacity: capacity}},
	})
	if err != nil {
		t.Fatal(err)
	}
	p.Checks = map[string][]string{"fix": {"grep -q fixed README.md"}}
	if err := runtime.Store().SavePlan(ctx, p, p.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Plans().Approve(ctx, projectID); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}

	s := startFrozenTUIInProject(t, 40, 160, bin, project, "tui")
	s.sendLine("/marshal model claude")
	s.mustSee("claude")
	s.sendLine("/marshal use-plan")
	if !s.waitFor("process 05: approval", 15*time.Second) {
		t.Fatalf("Process 05 did not reach its approval pause from PTY: %s", tail(s.output(), 3000))
	}
	match := regexp.MustCompile(`process 05: approval ([A-Za-z0-9-]+) required`).FindStringSubmatch(s.output())
	if len(match) != 2 {
		t.Fatalf("approval ID not shown: %s", tail(s.output(), 3000))
	}
	s.sendLine("/marshal approve-task " + match[1])
	s.mustSee("Process 05 task approved")
	// No resume is sent: the governed worker cannot run in this test.
}
