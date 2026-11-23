//go:build !governed

package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/cloud"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
)

func TestCLIDraftUsesEffectiveUltraTier(t *testing.T) {
	ctx := context.Background()
	repo := runtimeRepo(t)
	if _, err := Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	rt, err := Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	bin := t.TempDir()
	draftJSON := `{"tasks":[` +
		`{"id":"task-a","title":"write a","criteria":["a exists"],"paths":["src/a.txt"],"depends_on":[],"components":[],"worker":"codex","mode":"governed","type":"change","checks":[{"command":"test -f src/a.txt","criteria":["a exists"]}],"instructions":"write a","expected_output":"src/a.txt","control":"free"},` +
		`{"id":"task-b","title":"write b","criteria":["b exists"],"paths":["docs/b.txt"],"depends_on":[],"components":[],"worker":"claude","mode":"governed","type":"change","checks":[{"command":"test -f docs/b.txt","criteria":["b exists"]}],"instructions":"write b","expected_output":"docs/b.txt","control":"free"}]}`
	event, err := json.Marshal(map[string]any{
		"type": "item.completed",
		"item": map[string]any{"type": "agent_message", "text": draftJSON},
	})
	if err != nil {
		t.Fatal(err)
	}
	replyFile := filepath.Join(bin, "draft-reply.jsonl")
	if err := os.WriteFile(replyFile, append(event, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\ncat \"$MARSHAL_TEST_DRAFT_REPLY\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MARSHAL_TEST_DRAFT_REPLY", replyFile)

	entitled := cloud.Authorize(ctx, cloud.Config{}, t.TempDir(), "test").Gate
	s, err := rt.MarshalWired(MarshalWiring{
		Provider: "codex",
		Gate:     entitled,
		Approver: func(context.Context, string, string) (string, error) { return "owner", nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	// Default build with live ULTRA preference on -> CLI Draft and DraftFromProposal write plan.ModeUltra.
	cli := s.Model.(*MarshalCLI)
	cli.ProjectContext = nil
	cli.ConfigureKnowledgeMCP = nil
	draftCtx := requiredMarshalTestContext(t, cli)
	cliDraft, err := cli.Draft(draftCtx, "write disjoint files")
	if err != nil {
		t.Fatal(err)
	}
	if cliDraft.Plan.Mode != plan.ModeUltra {
		t.Fatalf("CLI draft mode = %q, want %q", cliDraft.Plan.Mode, plan.ModeUltra)
	}
	if len(cliDraft.Plan.MarshalWarnings) != 0 {
		t.Fatalf("exact disjoint draft produced unexpected warnings: %v", cliDraft.Plan.MarshalWarnings)
	}

	proposalDraft, err := s.DraftFromProposal([]byte(draftJSON), "codex")
	if err != nil {
		t.Fatal(err)
	}
	if proposalDraft.Plan.Mode != plan.ModeUltra {
		t.Fatalf("DraftFromProposal mode = %q, want %q", proposalDraft.Plan.Mode, plan.ModeUltra)
	}

	// Persisting ULTRA preference OFF drops both paths to plan.ModeStandard.
	if err := s.Store.SetUltraExecutionPreference(ctx, s.ProjectID, false); err != nil {
		t.Fatal(err)
	}
	offDraft, err := cli.Draft(draftCtx, "write disjoint files")
	if err != nil {
		t.Fatal(err)
	}
	if offDraft.Plan.Mode != plan.ModeStandard {
		t.Fatalf("CLI draft with preference off = %q, want %q", offDraft.Plan.Mode, plan.ModeStandard)
	}
	offProposal, err := s.DraftFromProposal([]byte(draftJSON), "codex")
	if err != nil {
		t.Fatal(err)
	}
	if offProposal.Plan.Mode != plan.ModeStandard {
		t.Fatalf("DraftFromProposal with preference off = %q, want %q", offProposal.Plan.Mode, plan.ModeStandard)
	}

	// Approving the ULTRA draft binds Process05Parallel and admits disjoint tasks concurrently.
	if err := s.Store.SetUltraExecutionPreference(ctx, s.ProjectID, true); err != nil {
		t.Fatal(err)
	}
	approved, err := cliDraft.Plan.Approve(time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if approved.ApprovedScope == nil || approved.ApprovedScope.Mode != plan.ModeUltra || approved.ApprovalScopeDigest != approved.ApprovedScope.Digest() {
		t.Fatalf("approved ULTRA plan lost mode binding: %+v", approved)
	}
	bound := marshal.Run{
		Process05Bound: true,
		Settings:       marshal.DefaultSettings(),
		Tasks:          cliDraft.Tasks,
	}
	bound.Tasks[0].State = marshal.Dispatched
	bound.Tasks[1].State = marshal.Queued
	bindPrivateProcess05Tier(&bound, approved)
	if !bound.Process05Parallel || bound.Tier != marshal.Ultra {
		t.Fatalf("approved ULTRA plan did not bind Process05Parallel: %+v", bound)
	}
	adm := EvaluateDispatchAdmission(s.EffectiveGate(), bound, bound.Tasks[1], 1)
	if !adm.Allowed {
		t.Fatalf("disjoint approved ULTRA task refused: %+v", adm)
	}
}

func TestBroadScopeSerializesWithNamedReason(t *testing.T) {
	ctx := context.Background()

	// 1. Draft warnings flag empty, glob, and unsafe paths without quoting raw draft values.
	badTasks := []marshal.Task{
		{PlanTaskID: "glob-task", Files: []string{"src/**"}},
		{PlanTaskID: "root-task", Files: []string{"."}},
		{PlanTaskID: "escape-task", Files: []string{"../secret-token"}},
		{PlanTaskID: "empty-task", Files: []string{""}},
		{PlanTaskID: "../unsafe-id", Files: []string{"*.go"}},
		{PlanTaskID: "exact-task", Files: []string{"./src/exact.go"}},
	}
	warnings := DraftFileScopeWarnings(badTasks)
	if len(warnings) != 5 {
		t.Fatalf("got %d warnings, want 5: %v", len(warnings), warnings)
	}
	for _, w := range warnings {
		if !strings.Contains(w, string(AdmissionScopeBroad)) || !strings.Contains(w, "exact project-relative file paths") {
			t.Fatalf("warning lacks named reason or suggestion: %q", w)
		}
		if strings.Contains(w, "secret-token") || strings.Contains(w, "../unsafe-id") {
			t.Fatalf("warning leaked unsafe draft text: %q", w)
		}
	}

	// Materialize attaches broad-scope warnings to draft.Plan.MarshalWarnings.
	cli := &MarshalCLI{
		ProjectID: "PROJECT-0123456789abcdef0123456789abcdef",
		Gate:      cloud.Authorize(ctx, cloud.Config{}, t.TempDir(), "test").Gate,
	}
	var prop marshalTaskProposal
	if err := json.Unmarshal([]byte(`{"tasks":[`+
		`{"id":"a","title":"broad","worker":"codex","mode":"governed","criteria":["a ok"],"paths":["src/**"],"checks":[{"command":"true","criteria":["a ok"]}]},`+
		`{"id":"b","title":"exact","worker":"claude","mode":"governed","criteria":["b ok"],"paths":["docs/b.txt"],"checks":[{"command":"true","criteria":["b ok"]}]}]}`), &prop); err != nil {
		t.Fatal(err)
	}
	draft, err := cli.materialize(prop, "", 1, []string{"codex", "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if draft.Plan.Mode != plan.ModeUltra {
		t.Fatalf("eligible default draft mode = %q, want %q", draft.Plan.Mode, plan.ModeUltra)
	}
	if len(draft.Plan.MarshalWarnings) != 1 || !strings.Contains(draft.Plan.MarshalWarnings[0], "task a") {
		t.Fatalf("unexpected draft warnings: %v", draft.Plan.MarshalWarnings)
	}

	// 2. Admission checks return typed codes with safe task IDs for all 5 serial blockers.
	gate := &collectorPreferenceGate{}
	gate.enabled.Store(true)
	baseRun := marshal.Run{
		Process05Bound:    true,
		Process05Parallel: true,
		Tier:              marshal.Ultra,
		Settings:          marshal.DefaultSettings(),
		Tasks: []marshal.Task{
			{PlanTaskID: "a", Mode: marshal.Governed, Worker: "codex", Files: []string{"src/**"}, State: marshal.Dispatched},
			{PlanTaskID: "b", Mode: marshal.Governed, Worker: "claude", Files: []string{"docs/b.txt"}, State: marshal.Queued},
		},
	}

	broadAdm := EvaluateDispatchAdmission(gate, baseRun, baseRun.Tasks[1], 1)
	if broadAdm.Allowed || broadAdm.Code != AdmissionScopeBroad || broadAdm.TaskID != "b" || !slices.Equal(broadAdm.BlockingTaskIDs, []string{"a"}) {
		t.Fatalf("broad admission = %+v, want scope_broad blocked by [a]", broadAdm)
	}
	err = checkDispatchAdmission(gate, baseRun, baseRun.Tasks[1], 1)
	var admErr *DispatchAdmissionError
	if !errors.As(err, &admErr) || admErr.Admission.Code != AdmissionScopeBroad {
		t.Fatalf("checkDispatchAdmission err = %v, want *DispatchAdmissionError(scope_broad)", err)
	}

	// Concrete normalized overlap -> scope_overlap.
	overlapRun := baseRun
	overlapRun.Tasks = []marshal.Task{
		{PlanTaskID: "a", Mode: marshal.Governed, Worker: "codex", Files: []string{"./src//shared.go"}, State: marshal.Dispatched},
		{PlanTaskID: "b", Mode: marshal.Governed, Worker: "claude", Files: []string{"src/shared.go"}, State: marshal.Queued},
	}
	overlapAdm := EvaluateDispatchAdmission(gate, overlapRun, overlapRun.Tasks[1], 1)
	if overlapAdm.Allowed || overlapAdm.Code != AdmissionScopeOverlap || !slices.Equal(overlapAdm.BlockingTaskIDs, []string{"a"}) {
		t.Fatalf("overlap admission = %+v, want scope_overlap blocked by [a]", overlapAdm)
	}

	// Unmerged dependency -> dependency_unmerged.
	depRun := baseRun
	depRun.Tasks = []marshal.Task{
		{PlanTaskID: "a", Mode: marshal.Governed, Worker: "codex", Files: []string{"src/a.go"}, State: marshal.Dispatched},
		{PlanTaskID: "b", Mode: marshal.Governed, Worker: "claude", Files: []string{"docs/b.txt"}, DependsOn: []string{"a"}, State: marshal.Queued},
	}
	depAdm := EvaluateDispatchAdmission(gate, depRun, depRun.Tasks[1], 1)
	if depAdm.Allowed || depAdm.Code != AdmissionDependencyUnmerged || !slices.Equal(depAdm.BlockingTaskIDs, []string{"a"}) {
		t.Fatalf("dep admission = %+v, want dependency_unmerged blocked by [a]", depAdm)
	}

	// Live preference OFF -> standard_tier.
	gate.enabled.Store(false)
	disjointRun := baseRun
	disjointRun.Tasks = []marshal.Task{
		{PlanTaskID: "a", Mode: marshal.Governed, Worker: "codex", Files: []string{"src/a.go"}, State: marshal.Dispatched},
		{PlanTaskID: "b", Mode: marshal.Governed, Worker: "claude", Files: []string{"docs/b.txt"}, State: marshal.Queued},
	}
	stdAdm := EvaluateDispatchAdmission(gate, disjointRun, disjointRun.Tasks[1], 1)
	if stdAdm.Allowed || stdAdm.Code != AdmissionStandardTier || !slices.Equal(stdAdm.BlockingTaskIDs, []string{"a"}) {
		t.Fatalf("standard tier admission = %+v, want standard_tier blocked by [a]", stdAdm)
	}

	// Unsafe task IDs are omitted from admission metadata.
	gate.enabled.Store(true)
	unsafeRun := baseRun
	unsafeRun.Tasks = []marshal.Task{
		{PlanTaskID: "../bad-a", Mode: marshal.Governed, Worker: "codex", Files: []string{"src/**"}, State: marshal.Dispatched},
		{PlanTaskID: "../bad-b", Mode: marshal.Governed, Worker: "claude", Files: []string{"docs/b.txt"}, State: marshal.Queued},
	}
	unsafeAdm := EvaluateDispatchAdmission(gate, unsafeRun, unsafeRun.Tasks[1], 1)
	if unsafeAdm.Allowed || unsafeAdm.Code != AdmissionScopeBroad || unsafeAdm.TaskID != "" || len(unsafeAdm.BlockingTaskIDs) != 0 || strings.Contains(unsafeAdm.Reason, "bad-") {
		t.Fatalf("unsafe IDs leaked into admission: %+v", unsafeAdm)
	}

	// 3. End-to-end Execute comparison: exact disjoint tasks run in parallel; widening one to a glob serializes them.
	runTwoTasks := func(t *testing.T, aFiles []string, wait time.Duration) (concurrent bool) {
		t.Helper()
		s, _ := setupExtremeService(t, 2)
		fake := s.Model.(marshalFakeModel)
		d := fake.draft
		d.Tasks[0].Worker, d.Tasks[0].DependsOn, d.Tasks[0].Files = "codex", nil, aFiles
		d.Plan.Tasks[0].DependsOn, d.Plan.Tasks[0].Paths = nil, aFiles
		d.Tasks[1].Worker, d.Tasks[1].DependsOn, d.Tasks[1].Files = "claude", nil, []string{"b.txt"}
		d.Plan.Tasks[1].DependsOn, d.Plan.Tasks[1].Paths = nil, []string{"b.txt"}
		g, err := plan.BuildGraph(d.Plan.Tasks)
		if err != nil {
			t.Fatal(err)
		}
		d.Plan.Graph = g
		s.Model = marshalFakeModel{draft: d, review: fake.review}

		var active atomic.Int32
		var maxActive atomic.Int32
		bothRunning := make(chan struct{})
		var closedOnce atomic.Bool
		makeDriver := func(provider string) driver.Governed {
			return driver.Governed{
				Provider: provider,
				Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
					cur := active.Add(1)
					for {
						old := maxActive.Load()
						if cur <= old || maxActive.CompareAndSwap(old, cur) {
							break
						}
					}
					if cur >= 2 {
						if closedOnce.CompareAndSwap(false, true) {
							close(bothRunning)
						}
					} else {
						select {
						case <-bothRunning:
						case <-time.After(wait):
						}
					}
					active.Add(-1)
					return nil, os.WriteFile(filepath.Join(req.Worktree, req.Task.PlanTaskID+".txt"), []byte("done\n"), 0o600)
				},
			}
		}
		s.Drivers["codex"] = makeDriver("codex")
		s.Drivers["claude"] = makeDriver("claude")

		if _, err := s.StartPlanning(ctx, "scope-run", "write files", marshal.Budget{}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Approve(ctx, "scope-run"); err != nil {
			t.Fatal(err)
		}
		final, err := s.Execute(ctx, "scope-run", marshalBrief, nil)
		if err != nil {
			t.Fatalf("execute failed: %v", err)
		}
		for _, task := range final.Tasks {
			if task.State != marshal.Merged {
				t.Fatalf("task %s not merged: %s", task.PlanTaskID, task.State)
			}
		}
		return maxActive.Load() >= 2
	}

	if !runTwoTasks(t, []string{"a.txt"}, 3*time.Second) {
		t.Fatal("exact disjoint ULTRA tasks did not run concurrently")
	}
	if runTwoTasks(t, []string{"a.txt", "src/**"}, 300*time.Millisecond) {
		t.Fatal("broad glob scope task ran concurrently with another task")
	}
}

func TestApprovedSerialPlanIsNotRebound(t *testing.T) {
	ctx := context.Background()
	p := plan.ExecutionPlan{
		ID:                  "PLAN-serial-1",
		ProjectID:           projectid.ID("PROJECT-0123456789abcdef0123456789abcdef"),
		Goal:                plan.GoalBinding{GoalID: "GOAL-serial", Revision: 1},
		Version:             1,
		State:               plan.StateReady,
		Mode:                plan.ModeStandard,
		ConstitutionVersion: constitution.Current,
		Tasks: []plan.Task{
			{ID: "a", Title: "task a", Paths: []string{"a.txt"}, Criteria: []string{"a done"}, Mutating: true, Weight: 1},
			{ID: "b", Title: "task b", Paths: []string{"b.txt"}, Criteria: []string{"b done"}, Mutating: true, Weight: 1},
		},
		Checks: map[string][]string{"a": {"test -f a.txt"}, "b": {"test -f b.txt"}},
	}
	graph, err := plan.BuildGraph(p.Tasks)
	if err != nil {
		t.Fatal(err)
	}
	p.Graph = graph
	approvedSerial, err := p.Approve(time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if approvedSerial.Mode != plan.ModeStandard || approvedSerial.ApprovedScope == nil || approvedSerial.ApprovedScope.Mode != plan.ModeStandard {
		t.Fatalf("approved serial plan corrupted: %+v", approvedSerial)
	}

	run := marshal.Run{
		PlanID:         approvedSerial.ID,
		PlanVersion:    approvedSerial.Version,
		Process05Bound: true,
		Settings:       marshal.DefaultSettings(),
		Tasks: []marshal.Task{
			{PlanTaskID: "a", Mode: marshal.Governed, Worker: "codex", Files: []string{"a.txt"}, State: marshal.Queued},
			{PlanTaskID: "b", Mode: marshal.Governed, Worker: "claude", Files: []string{"b.txt"}, State: marshal.Queued},
		},
	}
	bindPrivateProcess05Tier(&run, approvedSerial)
	if run.Process05Parallel || run.Tier != marshal.Standard {
		t.Fatalf("serial plan bound as parallel: %+v", run)
	}

	// Mutating plan.Mode after approval without a matching approved scope/digest cannot convert it.
	tampered := approvedSerial
	tampered.Mode = plan.ModeUltra
	bindPrivateProcess05Tier(&run, tampered)
	if run.Process05Parallel || run.Tier != marshal.Standard {
		t.Fatalf("tampered serial plan gained parallel binding: %+v", run)
	}

	// Unapproved READY plan with ModeUltra cannot bind Process05Parallel.
	unapproved := p
	unapproved.Mode = plan.ModeUltra
	bindPrivateProcess05Tier(&run, unapproved)
	if run.Process05Parallel || run.Tier != marshal.Standard {
		t.Fatalf("unapproved READY ULTRA plan gained parallel binding: %+v", run)
	}

	// Even with live ULTRA enabled, dispatch keeps the approved serial plan serial with code serial_plan.
	liveUltra := cloud.Authorize(ctx, cloud.Config{}, t.TempDir(), "test").Gate
	if marshal.TierPolicy(liveUltra, run.Settings).Tier != marshal.Ultra {
		t.Fatal("fixture gate is not ULTRA")
	}
	admQueued := EvaluateDispatchAdmission(liveUltra, run, run.Tasks[1], 1)
	if admQueued.Allowed || admQueued.Code != AdmissionSerialPlan || !slices.Equal(admQueued.BlockingTaskIDs, []string{"a"}) {
		t.Fatalf("serial plan admitted successor before predecessor merged: %+v", admQueued)
	}
	run.Tasks[0].State = marshal.Dispatched
	admActive := EvaluateDispatchAdmission(liveUltra, run, run.Tasks[1], 1)
	if admActive.Allowed || admActive.Code != AdmissionSerialPlan || !slices.Equal(admActive.BlockingTaskIDs, []string{"a"}) {
		t.Fatalf("serial plan admitted concurrent governed task under ULTRA gate: %+v", admActive)
	}
	blockers := DiagnoseSerialBlockers(liveUltra, run)
	if len(blockers) != 1 || blockers[0].Code != AdmissionSerialPlan || blockers[0].TaskID != "b" {
		t.Fatalf("DiagnoseSerialBlockers = %+v, want [serial_plan for b]", blockers)
	}

	// Amendment mode preservation keeps a serial Process 05 run in plan.ModeStandard even when live ULTRA is on.
	cli := &MarshalCLI{Gate: liveUltra}
	if got := cli.amendPlanMode(ctx, run); got != plan.ModeStandard {
		t.Fatalf("amendPlanMode converted serial run to %q", got)
	}
}
