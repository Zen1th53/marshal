//go:build linux

package tui

import (
	"context"
	"encoding/json"
	"os"
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
	startApprovedProcess05PTY(t, "gpt-test", "claude")
}

func startApprovedProcess05PTY(t *testing.T, modelName, reviewer string) *ptySession {
	t.Helper()
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
	profile := model.HarnessProfile{Harness: "codex", InstalledVersion: version, SupportedModels: []string{modelName}, DefaultModel: modelName, ProbeEvidenceID: "pty-process05-probe", ProbedAt: time.Now().UTC()}
	if err := runtime.Store().SaveHarnessProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.RegisterAgent(ctx, app.RegisterAgentRequest{Name: "pty governed codex", Role: model.RoleDeveloper, ModelProvider: "codex"}); err != nil {
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
		Tasks:             []plan.Task{{ID: "fix", Title: "Replace README.md contents with exactly fixed followed by a newline. Do not modify other files.", Mutating: true, Weight: 1, Paths: []string{"README.md"}, Criteria: goal.SuccessCriteria}},
		Candidates:        []goalintake.Candidate{{Provider: "codex", Model: modelName, Capacity: capacity, Governance: constitution.GovernanceVerified}},
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
	s.sendLine("/marshal model " + reviewer)
	s.mustSee(reviewer)
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
	return s
}

func TestPTYMarshalLiveProviderCompletesAfterApproval(t *testing.T) {
	if os.Getenv("MARSHAL_REAL_MODEL") != "1" {
		t.Skip("set MARSHAL_REAL_MODEL=1 to run Codex worker and Antigravity reviewer")
	}
	s := startApprovedProcess05PTY(t, "gpt-6-sol", "agy")
	s.sendLine("/marshal resume")
	deadline := time.Now().Add(4 * time.Minute)
	approvals := 0
	lastApproval := ""
	for time.Now().Before(deadline) {
		state := readProcess05PTYState(s.cmd.Dir)
		if state.TaskState == "COMPLETED_PENDING_VERIFY" {
			break
		}
		if state.TaskState == "FAILED" || state.State == "FAILED" {
			t.Fatalf("live worker failed: %s", state.Reason)
		}
		if state.TaskState == "NEEDS_APPROVAL" && state.ApprovalID != "" && state.ApprovalID != lastApproval {
			approvals++
			if approvals > 30 {
				t.Fatalf("live provider repeated approvals: %+v", state)
			}
			s.sendLine("/marshal approve-task " + state.ApprovalID)
			approvalDeadline := time.Now().Add(25 * time.Second)
			for time.Now().Before(approvalDeadline) {
				updated := readProcess05PTYState(s.cmd.Dir)
				if updated.ApprovalID != state.ApprovalID || updated.TaskState != "NEEDS_APPROVAL" {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			if updated := readProcess05PTYState(s.cmd.Dir); updated.ApprovalID == state.ApprovalID && updated.TaskState == "NEEDS_APPROVAL" {
				t.Fatalf("native approval was not accepted; state=%+v: %s", readProcess05PTYState(s.cmd.Dir), tail(s.output(), 4000))
			}
			lastApproval = state.ApprovalID
			continue
		}
		time.Sleep(250 * time.Millisecond)
	}
	state := readProcess05PTYState(s.cmd.Dir)
	if state.TaskState != "COMPLETED_PENDING_VERIFY" {
		t.Fatalf("live provider did not complete: %+v %s", state, tail(s.output(), 1200))
	}
	s.sendLine("/marshal resume")
	if !s.waitFor("verified · /marshal close", 30*time.Second) {
		t.Fatalf("completed live task was not verified: %+v %s", state, tail(s.output(), 2000))
	}
}

type process05PTYState struct {
	State      string
	TaskState  string
	ApprovalID string
	Reason     string
}

func readProcess05PTYState(project string) process05PTYState {
	paths, _ := filepath.Glob(filepath.Join(project, ".marshal", "execution", "runs", "*.json"))
	if len(paths) == 0 {
		return process05PTYState{}
	}
	data, err := os.ReadFile(paths[0])
	if err != nil {
		return process05PTYState{Reason: err.Error()}
	}
	var run struct {
		State string `json:"state"`
		Tasks map[string]struct {
			State      string `json:"state"`
			ApprovalID string `json:"approval_id"`
			Reason     string `json:"last_failure_reason"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(data, &run); err != nil {
		return process05PTYState{Reason: err.Error()}
	}
	task := run.Tasks["fix"]
	return process05PTYState{State: run.State, TaskState: task.State, ApprovalID: task.ApprovalID, Reason: task.Reason}
}
