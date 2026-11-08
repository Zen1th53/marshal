//go:build !governed

package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/statepath"
	"github.com/Zen1th53/marshal/internal/workerterminal"
	"github.com/Zen1th53/marshal/internal/worktree"
)

func TestExtremeActualGovernedApprovalHandleReportsWaitingWithoutFailure(t *testing.T) {
	w, runtime := realControlWorkspace(t, "SESSION-wait-alert", false)
	w.workDir = runtime.ProjectRoot()
	ctx := acceptedWorkerContext(t, runtime, t.Context())
	project := projectid.ID(runtime.ProjectIdentity())
	goal := model.GoalContract{ID: "GOAL-wait-alert", SessionID: w.sessionID, ProjectID: string(project), Revision: 1,
		OriginalRequest: "Fix README.md", Confirmation: model.ConfirmationApproved, ConstitutionVersion: constitution.Current.String(),
		DesiredOutcome: "README.md contains fixed", ExpectedArtifact: "README.md", SuccessCriteria: []string{"README.md contains fixed"},
		Risk: model.R1, AuthoritySource: "operator"}
	if err := runtime.Store().SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatal(err)
	}
	profile := model.HarnessProfile{Harness: "codex", InstalledVersion: "fixture-1", SupportedModels: []string{"fixture"}, DefaultModel: "fixture", ProbeEvidenceID: "wait-alert-probe", ProbedAt: time.Now().UTC()}
	if err := runtime.Store().SaveHarnessProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	capacity := goalintake.UnknownCapacity("codex", true)
	p, err := runtime.Plans().Create(ctx, app.CreatePlanRequest{SessionID: w.sessionID, ProjectID: project,
		Tasks:             []plan.Task{{ID: "fix", Title: "Fix README.md", Mutating: true, Weight: 1, Paths: []string{"README.md"}, Criteria: goal.SuccessCriteria}},
		Candidates:        []goalintake.Candidate{{Provider: "codex", Model: "fixture", Capacity: capacity, Governance: constitution.GovernanceVerified}},
		HarnessCandidates: []plan.HarnessCandidate{{Profile: profile, InstalledVersion: "fixture-1", Provider: "codex", Capacity: capacity}},
	})
	if err != nil {
		t.Fatal(err)
	}
	p.Checks = map[string][]string{"fix": {"grep -q fixed README.md"}}
	if err := runtime.Store().SavePlan(ctx, p, p.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Plans().Approve(ctx, project); err != nil {
		t.Fatal(err)
	}
	s, err := runtime.MarshalWired(app.MarshalWiring{Provider: "codex", Approver: func(context.Context, string, string) (string, error) { return "fixture-operator", nil }})
	if err != nil {
		t.Fatal(err)
	}
	s.InstalledVersion = func(context.Context, string) string { return "fixture-1" }
	run, err := s.BindApprovedPlan(ctx, "RUN-wait-alert")
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := worktree.New(runtime.ProjectRoot(), s.Worktrees).Prepare(ctx, model.WorktreeRequest{TaskID: "TASK-RUN-wait-alert-fix", Branch: run.Tasks[0].Branch, BaseCommit: run.Tasks[0].BaseCommit})
	if err != nil {
		t.Fatal(err)
	}
	request := driver.Request{RunID: "RUN-wait-alert", Task: run.Tasks[0], Worktree: prepared.Path, Brief: "execute the exact approved task"}
	a := &activeTmuxAgent{id: taskAgentID(request), role: "task", runID: request.RunID, taskID: "fix", provider: "codex", state: "working"}
	w.tmuxActiveWins[a.id] = a
	ctx = workerterminal.WithIdentity(ctx, func(identity workerterminal.Identity) error {
		w.tmuxMu.Lock()
		a.executionRunID, a.canonicalTaskID = identity.ExecutionRunID, identity.CanonicalTaskID
		w.tmuxMu.Unlock()
		return nil
	})
	governed := s.GovernedDrivers["codex"]
	h, err := governed.Launch(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	defer governed.Cancel(h)
	<-h.Done()
	record, originalErr, _ := h.Outcome()
	executionID, taskID, approvalID, pending := app.MarshalApprovalWaitBinding(originalErr)
	if !pending || executionID != a.executionRunID || taskID != "fix" || approvalID == "" || record.ExitCode == 0 {
		t.Fatalf("actual handle did not preserve a typed non-success approval wait: %+v %v", record, originalErr)
	}
	before, err := runtime.Execution().Engine().ApprovalManager().GetApproval(approvalID)
	if err != nil || before.Status != execution.ApprovalRequested {
		t.Fatalf("canonical live approval missing: %+v %v", before, err)
	}
	w.monitorTaskState(ctx, a, h)
	if a.state != "waiting" {
		t.Fatalf("completed orchestration handle misreported task state: %s", a.state)
	}
	wrapped := &tmuxTaskDriver{w: w, inner: governed}
	_, returnedErr := wrapped.Wait(ctx, h)
	if returnedErr != originalErr || a.cleaned || a.cleaning {
		t.Fatalf("waiting task lost original cause or closed its continuation: %v %+v", returnedErr, a)
	}
	data, err := os.ReadFile(filepath.Join(runtime.ProjectRoot(), statepath.DirName, "inbox", "marshal.md"))
	if err != nil || !strings.Contains(string(data), "Marshal is deciding approval "+approvalID) || !strings.Contains(string(data), "Run: "+request.RunID) || strings.Contains(string(data), "failed") || strings.Contains(string(data), "completed") || strings.Contains(string(data), "worker done") {
		t.Fatalf("canonical waiting alerts were misleading: %s %v", data, err)
	}
	after, err := runtime.Execution().Engine().ApprovalManager().GetApproval(approvalID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("presentation changed approval: %+v %v", after, err)
	}
	current, err := runtime.Execution().GetRun(ctx, executionID)
	task, found := current.Tasks[taskID]
	if err != nil || len(current.Tasks) != 1 || !found || task.State != execution.TaskNeedsApproval || task.Attempts != 0 || task.StartedAt != nil || task.ResultCommit != "" {
		t.Fatalf("worker launched before approval: %+v %v", current, err)
	}
	for _, mismatch := range []string{"parent", "task", "execution"} {
		copy := activeTmuxAgent{runID: a.runID, taskID: a.taskID, executionRunID: a.executionRunID}
		switch mismatch {
		case "parent":
			copy.runID = "RUN-other"
		case "task":
			copy.taskID = "other"
		case "execution":
			copy.executionRunID = "run-other"
		}
		if _, waiting := taskHandleApprovalWait(&copy, h, originalErr); waiting {
			t.Fatalf("%s mismatch borrowed an unrelated waiting state", mismatch)
		}
	}
	if _, waiting := taskHandleApprovalWait(a, h, errors.New(originalErr.Error())); waiting {
		t.Fatal("an untyped error string masqueraded as a canonical approval wait")
	}
	if _, waiting := taskHandleApprovalWait(a, h, errors.New("real worker failure")); waiting {
		t.Fatal("a real worker error became a waiting state")
	}
}
