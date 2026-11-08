package tui

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/learning"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/statepath"
	"github.com/Zen1th53/marshal/internal/verification"
)

func assertNoFixtureArtifacts(t *testing.T, r *app.Runtime) {
	t.Helper()
	artifacts, e := r.Artifacts(t.Context())
	if e != nil || len(artifacts) != 0 {
		t.Fatalf("refusal persisted artifacts: %d %v", len(artifacts), e)
	}
	entries, e := os.ReadDir(statepath.Path(r.ProjectRoot(), "worktrees"))
	if e != nil || len(entries) != 0 {
		t.Fatalf("refusal created worktree: %d %v", len(entries), e)
	}
}

// The custom harness has no runtime-owned context channel. An approved plan is
// insufficient to grant it execution or manufacture later verification data.
func assertUnsupportedAcceptance(t *testing.T, ws *Workspace, r *app.Runtime) {
	t.Helper()
	ctx := t.Context()
	project := projectid.ID(r.ProjectIdentity())
	ws.AttachRuntime(r, project)
	g := acceptanceGoal(ws)
	g.ID = "GOAL-unsupported-acceptance"
	g.SessionID = ws.sessionID
	g.ProjectID = string(project)
	g.Confirmation = model.ConfirmationApproved
	g.SuccessCriteria = []string{"fixture checked"}
	g.Scope = []string{"README.md"}
	if e := r.Store().SaveGoalContract(ctx, g, 0); e != nil {
		t.Fatal(e)
	}
	cap := goalintake.UnknownCapacity("codex", true)
	profile := model.HarnessProfile{Harness: "acceptance-harness", InstalledVersion: "1", SupportedModels: []string{"acceptance"}, DefaultModel: "acceptance", ProbeEvidenceID: "fixture-profile", ProbedAt: time.Now().UTC()}
	_, e := r.Plans().Create(ctx, app.CreatePlanRequest{SessionID: ws.sessionID, ProjectID: project, Tasks: []plan.Task{{ID: "fixture", Title: "fixture", Mutating: true, Weight: 1, Paths: g.Scope, Criteria: g.SuccessCriteria}}, Candidates: []goalintake.Candidate{{Provider: "codex", Model: "acceptance", Capacity: cap, Governance: constitution.GovernanceVerified}}, HarnessCandidates: []plan.HarnessCandidate{{Profile: profile, InstalledVersion: "1", Provider: "codex", Capacity: cap}}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.Plans().Approve(ctx, project); e != nil {
		t.Fatal(e)
	}
	var calls atomic.Int64
	s := r.Execution()
	s.RegisterHarness(execution.NewMockHarness("acceptance-harness", func(context.Context, execution.TaskExecution, execution.ConstraintPackage, string) (execution.TaskResult, error) {
		calls.Add(1)
		return execution.TaskResult{Success: true}, nil
	}))
	beforeGrants, e := r.Store().ListCapabilityGrants(ctx)
	if e != nil {
		t.Fatal(e)
	}
	run, e := s.StartRun(ctx, ws.sessionID, project)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.ExecuteRun(ctx, run.RunID)
	if !errors.Is(e, projectcontext.ErrReviewPending) {
		t.Fatalf("unsupported harness admitted: %v", e)
	}
	current, e := s.GetRun(ctx, run.RunID)
	if e != nil || current.State != execution.RunFailed || calls.Load() != 0 {
		t.Fatalf("missing zero-work refusal: state=%s calls=%d %v", current.State, calls.Load(), e)
	}
	for _, task := range current.Tasks {
		if task.State != execution.TaskFailed || task.Attempts != 0 || task.StartedAt != nil || task.WorktreePath != "" || task.ResultCommit != "" {
			t.Fatalf("unsupported harness acquired work: %+v", task)
		}
	}
	if _, e = r.Verification().CurrentForRun(ctx, run.RunID); !errors.Is(e, verification.ErrNotFound) {
		t.Fatalf("refusal created verification: %v", e)
	}
	if _, e = r.Store().GetMemoryCommit(ctx, "MEMORY-"+ws.sessionID); !errors.Is(e, learning.ErrNotFound) {
		t.Fatalf("refusal created learning commit: %v", e)
	}
	cycles, e := r.Store().ListOptimizationCycles(ctx)
	if e != nil || len(cycles) != 0 {
		t.Fatalf("refusal opened optimization: %d %v", len(cycles), e)
	}
	afterGrants, e := r.Store().ListCapabilityGrants(ctx)
	if e != nil || !reflect.DeepEqual(beforeGrants, afterGrants) {
		t.Fatal("unsupported harness acquired capability grants", e)
	}
	assertNoFixtureArtifacts(t, r)
	t.Log("PASS: unsupported private harness refused before work; no verification, learning or optimization grant")
}

func assignedFixtureSnapshot(t *testing.T, r *app.Runtime, id string) string {
	t.Helper()
	ctx := t.Context()
	task, e := r.Task(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	lease, e := r.Store().ActiveLease(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	session, e := r.Store().GetSession(ctx, lease.Lease.SessionID)
	if e != nil {
		t.Fatal(e)
	}
	grants, e := r.Store().ListCapabilityGrants(ctx)
	if e != nil {
		t.Fatal(e)
	}
	b, e := json.Marshal([]any{task, lease, session, grants})
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}

func assertUnreviewedDraftRefusedAndDiscarded(t *testing.T, w *Workspace, before chatBinding) {
	t.Helper()
	root := w.providerRoot()
	w.permissions.mu.Lock()
	pending, pendingRun := w.permissions.planApprovalPending, w.permissions.planApprovalRunID
	w.permissions.mu.Unlock()
	writeRecoveryChatDraft(t, root)
	_, e := w.marshalChat(t.Context())
	if e == nil || !strings.Contains(e.Error(), "REQUIRED_CONTEXT_REVIEW_PENDING") {
		t.Fatalf("raw draft bypassed native full-read admission: %v", e)
	}
	stopRecoveryDraftWatcher(w)
	w.permissions.mu.Lock()
	unchangedPending := w.permissions.planApprovalPending == pending && w.permissions.planApprovalRunID == pendingRun
	w.permissions.mu.Unlock()
	if !unchangedPending {
		t.Fatal("refused import changed pending approval intent")
	}
	if !reflect.DeepEqual(loadChatBinding(root), before) {
		t.Fatal("refused import changed chat binding")
	}
	if batch := w.permissions.queue.Take(false); len(batch) != 0 {
		t.Fatalf("unreviewed draft acquired approval: %+v", batch)
	}
	if _, e = w.runtime.Marshal().Store.GetMarshalRun(t.Context(), w.projectID, before.RunID); !errors.Is(e, model.ErrNotFound) {
		t.Fatalf("raw draft created canonical run: %v", e)
	}
	// A rewrite also cannot turn the unreviewed file into an approved plan.
	writeRecoveryChatDraft(t, root)
	if _, e = w.marshalApproveReviewed(t.Context(), nil); e == nil {
		t.Fatal("unreviewed rewrite approved")
	}
	if _, e = w.ExecuteCommand(t.Context(), "/marshal draft discard"); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{marshalDraftRelativePath, marshalDraftRelativePath + ".ready"} {
		if _, e = os.Lstat(filepath.Join(root, name)); !os.IsNotExist(e) {
			t.Fatalf("discard retained raw proposal: %v", e)
		}
	}
	if _, e = w.marshalChat(t.Context()); e != nil {
		t.Fatal(e)
	}
	stopRecoveryDraftWatcher(w)
	assertNoFixtureArtifacts(t, w.runtime)
}

func waitRequiredFixtureOperation(t *testing.T, w *Workspace) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case apply := <-w.uiEvents:
			apply()
		default:
		}
		m := w.marshalSession()
		m.mu.Lock()
		busy := m.busy
		m.mu.Unlock()
		if !busy {
			for {
				select {
				case apply := <-w.uiEvents:
					apply()
				default:
					return
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("private refusal operation did not finish")
}
