package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/projectid"
)

func TestM11MarshalUsageAndEmptyStatus(t *testing.T) {
	_, ws, ctx := acceptanceWorkspace(t)
	out, err := ws.ExecuteCommand(ctx, "/marshal help")
	if err != nil || !strings.Contains(out, "/marshal approve") {
		t.Fatalf("usage: %q %v", out, err)
	}
	if _, err := ws.ExecuteCommand(ctx, "/marshal"); err == nil || !strings.Contains(err.Error(), "requires an interactive terminal") {
		t.Fatalf("bare /marshal should launch a native session: %v", err)
	}
	out, err = ws.ExecuteCommand(ctx, "/marshal status")
	if err != nil || !strings.Contains(out, "No Marshal run") {
		t.Fatalf("status: %q %v", out, err)
	}
}

func TestMarshalProcess05ApprovalMustMatchActiveRun(t *testing.T) {
	run := marshal.Run{PlanID: "PLAN-1", PlanVersion: 2, BaseCommit: "base", Tasks: []marshal.Task{{PlanTaskID: "fix", Worker: "codex", Mode: marshal.Governed}}}
	p05 := execution.ExecutionRun{PlanID: "PLAN-1", PlanVersion: 2, ProjectID: projectid.ID("PROJECT-0123456789abcdef0123456789abcdef"), BaseCommit: "base", Delivery: execution.DeliveryPreserveBranch, State: execution.RunNeedsApproval, Tasks: map[string]execution.TaskExecution{"fix": {TaskID: "fix", AssignedHarness: "codex", State: execution.TaskNeedsApproval, ApprovalID: "approval-1"}}}
	project := string(p05.ProjectID)
	if !marshalProcess05ApprovalBound(run, p05, project, "approval-1") {
		t.Fatal("bound approval was refused")
	}
	if marshalProcess05ApprovalBound(run, p05, project, "other") {
		t.Fatal("unrelated approval was accepted")
	}
	p05.BaseCommit = "different"
	if marshalProcess05ApprovalBound(run, p05, project, "approval-1") {
		t.Fatal("approval for another base commit was accepted")
	}
}

// /marshal returns control to the composer before planning finishes, in a
// Standard session with no ULTRA gate.
func TestM11MarshalReturnsBeforePlanningFinishes(t *testing.T) {
	_, ws, ctx := acceptanceWorkspace(t)
	if ws.ultra != nil {
		t.Fatal("the fixture must be a Standard session")
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\nsleep 3\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err := ws.ExecuteCommand(ctx, "/marshal model codex"); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	out, err := ws.ExecuteCommand(ctx, "/marshal add a greeting file")
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("/marshal blocked for %s", elapsed)
	}
	if !strings.Contains(out, "drafting") {
		t.Fatalf("out = %q", out)
	}
	p := ws.marshalPanel()
	if p == nil || p.State != marshal.Drafting || p.Provider != "codex" {
		t.Fatalf("panel = %+v", p)
	}
	// The fake Marshal model fails; the panel reports it once planning ends.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if p := ws.marshalPanel(); p != nil && strings.HasPrefix(p.Note, "planning failed") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the panel never reported the planning outcome: %+v", ws.marshalPanel())
}

// Approvals come only from the person's explicit commands and are spent by
// the decision they were given for.
func TestM11ApprovalOnlyFromExplicitCommand(t *testing.T) {
	m := &marshalSession{}
	if _, err := m.approver(context.Background(), "RUN-1", "plan"); err == nil {
		t.Fatal("an approval was answered without the person giving one")
	}
	m.grant("RUN-1", "plan")
	who, err := m.approver(context.Background(), "RUN-1", "plan")
	if err != nil || !strings.HasPrefix(who, "operator") {
		t.Fatalf("approver = %q %v", who, err)
	}
	if _, err := m.approver(context.Background(), "RUN-1", "plan"); err == nil {
		t.Fatal("an approval was used twice")
	}
	m.grant("RUN-1", "plan")
	if _, err := m.approver(context.Background(), "RUN-1", "close"); err == nil {
		t.Fatal("a plan approval answered a close")
	}
}

// The panel shows each task with its worker, state and returns.
func TestM11PanelShowsTasks(t *testing.T) {
	run := marshal.Run{State: marshal.Dispatching, Tier: marshal.Standard, Tasks: []marshal.Task{
		{PlanTaskID: "write-api", Worker: "codex", State: marshal.Dispatched},
		{PlanTaskID: "write-docs", Worker: "claude", State: marshal.Returned, ReturnsByAgent: map[string]int{"claude": 1}},
	}}
	p := newMarshalPanel("RUN-1", "claude", run, "running")
	th := NewTheme(ThemeDefault, false, false)
	text := strings.Join(marshalSection(UIState{Marshal: p}, th, 100), "\n")
	for _, want := range []string{"Marshal", "RUN-1", "write-api", "codex", "write-docs", "returned 1", "running"} {
		if !strings.Contains(text, want) {
			t.Fatalf("panel is missing %q:\n%s", want, text)
		}
	}
	if marshalSection(UIState{}, th, 100) != nil {
		t.Fatal("no run must paint nothing")
	}
}

func TestM11StatusShowsProposedTaskScope(t *testing.T) {
	run := marshal.Run{State: marshal.Drafting, Tasks: []marshal.Task{{PlanTaskID: "write", Worker: "codex", Criteria: []string{"file exists"}, Files: []string{"hello.txt"}, Checks: []marshal.Check{{Command: "test -f hello.txt"}}}}}
	status := marshalStatusText(newMarshalPanel("RUN-1", "claude", run, "review before approval"))
	for _, want := range []string{"file exists", "hello.txt", "test -f hello.txt"} {
		if !strings.Contains(status, want) {
			t.Fatalf("proposal scope missing %q: %s", want, status)
		}
	}
}

func TestM11SettingsRoundTrip(t *testing.T) {
	_, ws, ctx := acceptanceWorkspace(t)
	if _, err := ws.ExecuteCommand(ctx, "/marshal settings acceptance-mode marshal"); err != nil {
		t.Fatal(err)
	}
	out, err := ws.ExecuteCommand(ctx, "/marshal settings")
	if err != nil || !strings.Contains(out, "acceptance-mode marshal") {
		t.Fatalf("settings: %q %v", out, err)
	}
	if _, err := ws.ExecuteCommand(ctx, "/marshal settings acceptance-mode anyone"); err == nil {
		t.Fatal("an invalid acceptance mode was stored")
	}
}

// The worker brief names the files it may change and the checks it will be
// judged by.
func TestM11TaskBriefNamesScopeAndChecks(t *testing.T) {
	brief := marshalTaskBrief(marshal.Task{PlanTaskID: "T1", Title: "Fix the build", Files: []string{"a.go"}, Criteria: []string{"builds"}, Checks: []marshal.Check{{Command: "go build ./..."}}})
	for _, want := range []string{"T1", "Fix the build", "a.go", "builds", "go build ./...", "Do not push"} {
		if !strings.Contains(brief, want) {
			t.Fatalf("brief is missing %q:\n%s", want, brief)
		}
	}
}

func TestM11BudgetRenderingShowsCeilingsAndUnknownUsage(t *testing.T) {
	run := marshal.Run{State: marshal.Drafting, Budget: marshal.Budget{Tokens: marshal.Ceiling{Task: 100, Plan: 200}, Money: marshal.Ceiling{Task: 5, Plan: 10}, WallTime: marshal.Ceiling{Task: 60, Plan: 120}}}
	p := newMarshalPanel("RUN-1", "codex", run, "waiting")
	rendered := strings.Join(marshalSection(UIState{Marshal: p}, NewTheme(ThemeDefault, false, false), 180), "\n")
	for _, want := range []string{"tokens unknown (task 100 / plan 200)", "money unknown (task 5 / plan 10)", "wall 0s (task 60 / plan 120)"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("missing %q in %s", want, rendered)
		}
	}
}

func TestM11StandardDecisionCommands(t *testing.T) {
	_, ws, ctx := acceptanceWorkspace(t)
	if ws.ultra != nil {
		t.Fatal("expected Standard session")
	}
	m := ws.marshalSession()
	m.mu.Lock()
	m.runID = "RUN-1"
	m.amended = true
	m.pending = &marshalAmendment{reason: "proposal"}
	m.mu.Unlock()
	ws.setMarshalPanel(&MarshalPanel{RunID: "RUN-1", State: marshal.Drafting, Tasks: []MarshalTaskRow{{ID: "task-1"}}})
	if _, err := ws.ExecuteCommand(ctx, "/marshal accept missing"); err == nil {
		t.Fatal("accepted an unknown task")
	}
	if _, err := ws.ExecuteCommand(ctx, "/marshal accept task-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.approver(ctx, "RUN-1", "task-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.approver(ctx, "RUN-1", "task-1"); err == nil {
		t.Fatal("task approval reused")
	}
	if _, err := ws.ExecuteCommand(ctx, "/marshal amend deny"); err != nil {
		t.Fatal(err)
	}
	if p := ws.marshalPanel(); p == nil || !strings.Contains(p.Note, "amendment denied") {
		t.Fatalf("denial not shown: %+v", p)
	}
	m.mu.Lock()
	pending := m.pending
	m.mu.Unlock()
	if pending != nil {
		t.Fatal("denied amendment remains pending")
	}
	if _, err := ws.ExecuteCommand(ctx, "/marshal amend approve"); err == nil {
		t.Fatal("denied amendment was approved")
	}
}

func TestM11WorkspaceStateReachesPanelWithinOneRepaint(t *testing.T) {
	_, ws, ctx := acceptanceWorkspace(t)
	m := ws.marshalSession()
	m.mu.Lock()
	m.runID = "RUN-1"
	m.amended = true
	m.mu.Unlock()
	ws.setMarshalPanel(&MarshalPanel{RunID: "RUN-1", State: marshal.Drafting, Note: "awaiting decision"})
	if _, err := ws.ExecuteCommand(ctx, "/marshal amend deny"); err != nil {
		t.Fatal(err)
	}
	if p := ws.marshalPanel(); p == nil || !strings.Contains(p.Note, "amendment denied") {
		t.Fatalf("command state missed repaint: %+v", p)
	}
	ws.marshalPublish(m, "OBSOLETE", &MarshalPanel{RunID: "OBSOLETE", Note: "stale"})
	if p := ws.marshalPanel(); p.RunID != "RUN-1" {
		t.Fatalf("obsolete update won: %+v", p)
	}
}

func TestM11ExecutionReservationIsExclusive(t *testing.T) {
	m := &marshalSession{}
	_, cancel, err := m.reserve()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.reserve(); err == nil {
		t.Fatal("second execution reserved")
	}
	m.finish(cancel)
	_, cancel, err = m.reserve()
	if err != nil {
		t.Fatal(err)
	}
	m.finish(cancel)
}
