//go:build !governed

package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
)

// Real queue/router + durable approval objects + fake owned terminal transport.
// This is model-free UI qualification, not a fabricated native approval.
func TestProcess05SamePollTransportQuestionsReachOneNativeTurn(t *testing.T) {
	w, requests := process05QuestionFixture(t)
	log, _ := fakeCodexDecisionPane(t, "pasted")
	for _, req := range requests {
		w.queuePermission(req)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { w.runPermissionQueue(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		w.permissions.mu.Lock()
		delivered := 0
		for _, p := range w.permissions.build.pending {
			if p.delivered {
				delivered++
			}
		}
		w.permissions.mu.Unlock()
		if delivered == 4 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	w.permissions.mu.Lock()
	var ids []string
	for id, p := range w.permissions.build.pending {
		if !p.delivered {
			w.permissions.mu.Unlock()
			t.Fatalf("same-poll question %s was stranded behind first native turn", id)
		}
		if p.answer != nil {
			w.permissions.mu.Unlock()
			t.Fatal("question presentation manufactured decision")
		}
		ids = append(ids, id)
	}
	w.permissions.mu.Unlock()
	if len(ids) != 4 {
		t.Fatalf("original individually pending questions=%d", len(ids))
	}

	pastes, submits := 0, 0
	var payload string
	for _, call := range decisionCalls(t, log) {
		switch call.Args[0] {
		case "set-buffer":
			payload = call.Args[len(call.Args)-1]
		case "paste-buffer":
			pastes++
		case "send-keys":
			if call.Args[len(call.Args)-1] == "Enter" {
				submits++
			}
		}
	}
	if pastes != 1 || submits != 1 {
		t.Fatalf("expected one native turn, got paste=%d submit=%d", pastes, submits)
	}
	for _, id := range ids {
		if !strings.Contains(payload, id) {
			t.Fatal("batch omitted original individual decision id")
		}
	}
}

// Production OpenTaskLane establishes every parent/child task binding. Approval
// objects are model-free fixture requests; no provider or native turn starts.
func process05QuestionFixture(t *testing.T) (*Workspace, []permission.Request) {
	t.Helper()
	return process05QuestionFrontierFixture(t, false, false)
}
func process05QuestionFrontierFixture(t *testing.T, missingWait, blocked bool, reviewOptions ...process05ReviewFixtureOptions) (*Workspace, []permission.Request) {
	t.Helper()
	w := startupCodexDecisionWorkspace(t)
	rt := w.runtime
	ctx := t.Context()
	goal := model.GoalContract{ID: "GOAL-four-questions", SessionID: w.sessionID, ProjectID: rt.ProjectIdentity(), Revision: 1, OriginalRequest: "bounded fixture", DesiredOutcome: "bounded fixture", Confirmation: model.ConfirmationApproved, ConstitutionVersion: constitution.Current.String(), Risk: model.R1, AuthoritySource: "fixture"}
	var review process05ReviewFixtureOptions
	if len(reviewOptions) > 0 {
		review = reviewOptions[0]
		goal.Constraints = review.constraints
		goal.DoNotDo = review.doNotDo
	}
	if err := rt.Store().SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatal(err)
	}
	e := rt.Execution().Engine()
	parent := execution.ExecutionRun{RunID: "parent-four-questions", ProjectID: projectid.ID(rt.ProjectIdentity()), SessionID: w.sessionID, GoalID: goal.ID, GoalRevision: 1, PlanID: "PLAN-four", PlanVersion: 2, Mode: plan.ModeUltra, Delivery: execution.DeliveryPreserveBranch, BaseCommit: "fixture-base", State: execution.RunReady, Tasks: map[string]execution.TaskExecution{}, Provenance: execution.RunProvenance{RepoRoot: rt.ProjectRoot()}}
	parent.StartedAt = review.started
	parent.BudgetConsumed.WallClockMs = review.planChargeMs
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("task-%d", i)
		parent.Tasks[id] = execution.TaskExecution{RunID: parent.RunID, TaskID: id, CanonicalTaskID: fmt.Sprintf("TASK-fixture-%d", i), State: execution.TaskPending, AssignedHarness: "codex", AssignedModel: "fixture", TargetFiles: []string{fmt.Sprintf("marker-%d.txt", i)}}
	}
	if blocked {
		task := parent.Tasks["task-3"]
		task.Dependencies = []string{"task-0"}
		parent.Tasks["task-3"] = task
	}
	if err := e.RunStore().CreateRun(ctx, parent); err != nil {
		t.Fatal(err)
	}
	service := rt.Marshal()
	run := marshal.Run{PlanID: parent.PlanID, PlanVersion: 2, Process05Bound: true, Process05Parallel: true, BaseCommit: parent.BaseCommit, Repository: rt.ProjectRoot(), Settings: marshal.DefaultSettings(), State: marshal.AwaitingUser, Budget: marshal.Budget{WallTime: marshal.Ceiling{Task: 300, Plan: 900}}}
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("task-%d", i)
		if missingWait && i == 3 {
			future := marshal.Task{PlanTaskID: id, Worker: "codex", Mode: marshal.Governed, State: marshal.Queued, BaseCommit: parent.BaseCommit}
			if blocked {
				future.DependsOn = []string{"task-0"}
			}
			run.Tasks = append(run.Tasks, future)
			continue
		}
		lane, err := e.OpenTaskLane(ctx, parent.RunID, id, parent.BaseCommit)
		if err != nil {
			t.Fatal(err)
		}
		ttl := time.Minute
		if review.ttl != 0 {
			ttl = review.ttl
		}
		a, err := e.ApprovalManager().RequestApproval(execution.ApprovalRequest{RunID: lane.RunID, TaskID: id, PlanID: parent.PlanID, PlanVersion: 2, OperationType: "PROVIDER_TRANSPORT", TargetResource: "chatgpt.com:443", Scope: "fixture exact binding " + id, TTL: ttl})
		if err != nil {
			t.Fatal(err)
		}
		task := lane.Tasks[id]
		task.State = execution.TaskNeedsApproval
		task.ApprovalID = a.ApprovalID
		lane.Tasks[id] = task
		lane.State = execution.RunNeedsApproval
		lane.BudgetConsumed.WallClockMs = review.taskChargeMs
		if err := e.RunStore().UpdateRun(ctx, *lane); err != nil {
			t.Fatal(err)
		}
		run.Tasks = append(run.Tasks, marshal.Task{PlanTaskID: id, Worker: "codex", Mode: marshal.Governed, State: marshal.Queued, BaseCommit: parent.BaseCommit, Process05WaitRunID: lane.RunID, Process05WaitApprovalID: a.ApprovalID})
	}
	run.Settings.Budget = run.Budget
	if review.taskSeconds != 0 {
		run.Budget.WallTime.Task = review.taskSeconds
		run.Settings.Budget = run.Budget
	}
	revision, err := service.Store.SetMarshalRun(ctx, service.ProjectID, "RUN-four-questions", run, 0)
	if err != nil {
		t.Fatal(err)
	}
	m := w.marshalSession()
	m.mu.Lock()
	m.service = service
	m.runID = "RUN-four-questions"
	m.provider = "codex"
	m.mu.Unlock()
	var requests []permission.Request
	for _, task := range run.Tasks {
		if task.Process05WaitApprovalID == "" {
			continue
		}
		req := permission.Request{Kind: "marshal-command", Object: "/marshal approve-task " + task.Process05WaitApprovalID, RunID: m.runID, TaskID: marshalRunProposalBinding(run.PlanVersion, revision, nil), Scope: "exact current run"}
		w.describePrivateTransportApproval(&req, task.Process05WaitApprovalID)
		requests = append(requests, req)
	}
	return w, requests
}

func TestProcess05PartialAppliedTransportCannotQueuePrematureResume(t *testing.T) {
	w, requests := process05QuestionFixture(t)
	a, err := w.runtime.Execution().Engine().ApprovalManager().GetApproval(strings.TrimPrefix(requests[0].Object, "/marshal approve-task "))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.runtime.Execution().Engine().DecideApproval(t.Context(), a.ApprovalID, true, "fixture-decider", "fixture, not native proof"); err != nil {
		t.Fatal(err)
	}
	w.queuePrivateProcess05Resume()
	for _, req := range w.permissions.queue.Take(false) {
		if req.Object == "/marshal resume" {
			t.Fatal("one of four approvals queued premature run resume without all applied native receipts")
		}
	}
}

// The local authenticated MCP protocol simulates independent native answers;
// these fixture answers are not real model judgments or release acceptance.
func TestProcess05AllAppliedNativeReviewsQueueOneCurrentResumeSnapshot(t *testing.T) {
	w, resume := appliedProcess05QuestionFixture(t, false, false)
	if resume.Object == "" || resume.ApprovalBinding == "" || !strings.Contains(resume.Detail, "ParentState") || !strings.Contains(resume.Reason, "pre-execution") {
		t.Fatalf("current readiness was not visible: %+v", resume)
	}
	var snapshot process05ResumeSnapshot
	if err := json.Unmarshal([]byte(resume.ApprovalBinding), &snapshot); err != nil || len(snapshot.Lanes) != 4 {
		t.Fatalf("snapshot failed: %+v %v", snapshot, err)
	}
	for _, lane := range snapshot.Lanes {
		if lane.State != string(execution.RunPaused) || lane.TaskState != string(execution.TaskReady) || lane.NativeReceipt == "" {
			t.Fatal("snapshot invented readiness")
		}
	}
	if err := w.validatePrivateProcess05Resume(t.Context(), resume, marshalProposal{action: "resume", who: "MARSHAL runtime"}); err != nil {
		t.Fatal(err)
	}
	w.queuePrivateProcess05Resume()
	if len(w.permissions.queue.Take(false)) != 0 {
		t.Fatal("unchanged question duplicated")
	}
	lane, err := w.runtime.Execution().Engine().GetRun(t.Context(), snapshot.Lanes[0].Run)
	if err != nil {
		t.Fatal(err)
	}
	lane.UpdatedAt = time.Now().UTC()
	if err := w.runtime.Execution().Engine().RunStore().UpdateRun(t.Context(), lane); err != nil {
		t.Fatal(err)
	}
	if err := w.applyMarshalProposal(t.Context(), resume); err == nil {
		t.Fatal("changed child state authorized stale run continuation")
	}
}

func appliedProcess05QuestionFixture(t *testing.T, missing, blocked bool) (*Workspace, permission.Request) {
	t.Helper()
	w, original := process05QuestionFrontierFixture(t, missing, blocked)
	cfg := bindWorkspaceMCP(t, w)
	fakeCodexDecisionPane(t, "pasted")
	for _, req := range original {
		if err := w.queueMarshalRunProposal(marshalProposal{action: "approve-task", id: strings.TrimPrefix(req.Object, "/marshal approve-task "), who: "MARSHAL runtime"}); err != nil {
			t.Fatal(err)
		}
	}
	questions := w.permissions.queue.Take(false)
	ctx := context.WithValue(t.Context(), privateDecisionRegistrationKey{}, true)
	for _, req := range questions {
		w.routeBuildPermission(ctx, req)
	}
	var resume permission.Request
	for i, req := range questions {
		id := ""
		w.permissions.mu.Lock()
		for candidate, p := range w.permissions.build.pending {
			if p.request.Key() == req.Key() {
				id = candidate
			}
		}
		w.permissions.mu.Unlock()
		if id == "" {
			t.Fatal("individual question missing")
		}
		submitMCP(t, cfg, fmt.Sprintf(`{"submission_id":"review-%d","proposal":{"action":"decide","id":%q,"allow":true,"reason":"model-free protocol fixture exact independent review"}}`, i, id))
		w.routeBuildPermission(ctx, req)
		approvalID := strings.TrimPrefix(req.Object, "/marshal approve-task ")
		if _, err := w.store.AppliedMCPTransportReview(t.Context(), w.projectID, approvalID); err != nil {
			t.Fatalf("actual protected application missing: %v", err)
		}
		batch := w.permissions.queue.Take(false)
		for _, q := range batch {
			if q.Object == "/marshal resume" {
				if i != len(questions)-1 {
					t.Fatal("partial review queued resume")
				}
				if resume.Object != "" {
					t.Fatal("duplicate automatic resume question")
				}
				resume = q
			}
		}
	}

	return w, resume
}

func TestProcess05RunnableMissingWaitRefusesButBlockedFutureDoesNotDeadlock(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		t.Run(fmt.Sprintf("dependency_blocked_%v", blocked), func(t *testing.T) {
			w, resume := appliedProcess05QuestionFixture(t, true, blocked)
			_, ready := w.privateProcess05ResumeSnapshot(t.Context())
			if ready != blocked || (resume.Object != "") != blocked {
				t.Fatalf("ready=%v resume=%v blocked=%v", ready, resume.Object != "", blocked)
			}
		})
	}
}

func TestProcess05StaleTransportIntentNeverFallsBackToOrdinaryDelivery(t *testing.T) {
	w, requests := process05QuestionFixture(t)
	log, _ := fakeCodexDecisionPane(t, "pasted")
	w.registerPrivateProcess05Questions(t.Context(), requests)
	w.permissions.mu.Lock()
	var id string
	var p *buildPendingDecision
	for key, value := range w.permissions.build.pending {
		id, p = key, value
		break
	}
	w.permissions.mu.Unlock()
	if p == nil || !w.privateProcess05Question(p.request) {
		t.Fatal("fixture missing original current transport")
	}
	m := w.marshalSession()
	m.mu.Lock()
	m.runID = "RUN-replacement"
	m.mu.Unlock()
	w.deliverCodexDecision(t.Context(), id, p)
	for _, call := range decisionCalls(t, log) {
		if call.Args[0] == "set-buffer" || call.Args[0] == "paste-buffer" || call.Args[0] == "send-keys" {
			t.Fatal("stale transport entered ordinary native delivery")
		}
	}
}

func TestProcess05OversizePacketRefusesEveryQuestionWithoutTruncation(t *testing.T) {
	w, requests := process05QuestionFixture(t)
	log, _ := fakeCodexDecisionPane(t, "pasted")
	for i := range requests {
		requests[i].Scope += "; exact detail " + strings.Repeat("x", privateDecisionPacketBytes/3)
	}
	w.registerPrivateProcess05Questions(t.Context(), requests)
	w.permissions.mu.Lock()
	var id string
	var p *buildPendingDecision
	for key, value := range w.permissions.build.pending {
		id, p = key, value
		break
	}
	w.permissions.mu.Unlock()
	items, payload, bounded := w.privateDecisionPacket(id, p)
	if bounded || len(items) != 4 || len(payload) <= privateDecisionPacketBytes {
		t.Fatal("fixture did not retain exact oversized scopes")
	}
	w.deliverCodexDecision(t.Context(), id, p)
	for _, call := range decisionCalls(t, log) {
		if call.Args[0] == "set-buffer" || call.Args[0] == "paste-buffer" || call.Args[0] == "send-keys" {
			t.Fatal("oversized scopes entered native input")
		}
	}
	w.permissions.mu.Lock()
	remaining := len(w.permissions.build.pending)
	w.permissions.mu.Unlock()
	if remaining != 0 {
		t.Fatal("oversize left unaccounted batch callbacks")
	}
	events, err := w.store.ListEvents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	denied := 0
	for _, event := range events {
		if event.Type == "PERMISSION_DECIDED" && event.Data["reason"] == "decision packet too large" && event.Data["allow"] == false {
			denied++
		}
	}
	if denied != 4 {
		t.Fatalf("independent oversize outcomes=%d", denied)
	}
}

func TestProcess05LateQuestionsWaitThenRetainOriginalIndependentDeadlines(t *testing.T) {
	w, requests := process05QuestionFixture(t)
	cfg := bindWorkspaceMCP(t, w)
	log, mode := fakeCodexDecisionPane(t, "pasted")
	w.registerPrivateProcess05Questions(t.Context(), requests[:2])
	find := func(req permission.Request) (string, *buildPendingDecision) {
		w.permissions.mu.Lock()
		defer w.permissions.mu.Unlock()
		for id, p := range w.permissions.build.pending {
			if p.request.Key() == req.Key() {
				return id, p
			}
		}
		return "", nil
	}
	id, p := find(requests[0])
	w.deliverCodexDecision(t.Context(), id, p)
	w.registerPrivateProcess05Questions(t.Context(), requests[2:])
	lateID, late := find(requests[2])
	deadline := late.deadline
	w.deliverCodexDecision(t.Context(), lateID, late)
	if late.delivered || !late.deadline.Equal(deadline) {
		t.Fatal("late question injected into active native group or deadline extended")
	}
	for i, req := range requests[:2] {
		id, _ := find(req)
		submitMCP(t, cfg, fmt.Sprintf(`{"submission_id":"decline-%d","proposal":{"action":"decide","id":%q,"allow":false,"reason":"model-free fixture genuine protocol decline"}}`, i, id))
		w.routeBuildPermission(context.WithValue(t.Context(), privateDecisionRegistrationKey{}, true), req)
	}
	if err := os.WriteFile(mode, []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	w.deliverCodexDecision(t.Context(), lateID, late)
	if !late.delivered || !late.deadline.Equal(deadline) {
		t.Fatal("late current group did not retain original deadline")
	}
	pastes := 0
	for _, call := range decisionCalls(t, log) {
		if call.Args[0] == "paste-buffer" {
			pastes++
		}
	}
	if pastes != 2 {
		t.Fatalf("late groups pasted=%d", pastes)
	}
}

func TestProcess05CanonicalChangeAfterPasteRefusesEnter(t *testing.T) {
	w, requests := process05QuestionFixture(t)
	log, _ := fakeCodexDecisionPane(t, "pasted")
	w.registerPrivateProcess05Questions(t.Context(), requests)
	w.permissions.mu.Lock()
	var id string
	var p *buildPendingDecision
	for key, value := range w.permissions.build.pending {
		id, p = key, value
		break
	}
	w.permissions.mu.Unlock()
	changed := make(chan bool, 1)
	go func() {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			data, _ := os.ReadFile(log)
			if strings.Contains(string(data), "paste-buffer") {
				m := w.marshalSession()
				m.mu.Lock()
				m.runID = "RUN-replacement"
				m.mu.Unlock()
				changed <- true
				return
			}
			time.Sleep(time.Millisecond)
		}
		changed <- false
	}()
	w.deliverCodexDecision(t.Context(), id, p)
	if !<-changed {
		t.Fatal("fixture did not change between paste and submit")
	}
	for _, call := range decisionCalls(t, log) {
		if call.Args[0] == "send-keys" {
			t.Fatal("changed canonical binding submitted native packet")
		}
	}
}

func TestProcess05UnavailableDurableEngineCannotQueueResume(t *testing.T) {
	w := startupCodexDecisionWorkspace(t)
	runDir := filepath.Join(w.runtime.StateDirectory(), "execution", "runs")
	if err := os.MkdirAll(filepath.Dir(runDir), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runDir, []byte("unreadable run-store directory"), 0600); err != nil {
		t.Fatal(err)
	}
	service := w.runtime.Marshal()
	run := marshal.Run{PlanID: "PLAN-unavailable", PlanVersion: 2, Process05Bound: true, Process05Parallel: true, State: marshal.AwaitingUser, Settings: marshal.DefaultSettings()}
	run.Budget = run.Settings.Budget
	run.Tasks = []marshal.Task{{PlanTaskID: "task-unavailable", Worker: "codex", Mode: marshal.Governed, State: marshal.Queued, Process05WaitRunID: "lane-unavailable", Process05WaitApprovalID: "approval-unavailable"}}
	if _, err := service.Store.SetMarshalRun(t.Context(), service.ProjectID, "RUN-unavailable", run, 0); err != nil {
		t.Fatal(err)
	}
	m := w.marshalSession()
	m.mu.Lock()
	m.service = service
	m.runID = "RUN-unavailable"
	m.mu.Unlock()
	if w.runtime.Execution().Engine() != nil {
		t.Fatal("fixture did not fail durable store initialization")
	}
	w.queuePrivateProcess05Resume()
	if _, ready := w.privateProcess05ResumeSnapshot(t.Context()); ready {
		t.Fatal("unavailable execution engine invented readiness")
	}
	if len(w.permissions.queue.Take(false)) != 0 {
		t.Fatal("unavailable engine queued resume authority")
	}
}

func TestProcess05PresentationFailurePreservesAuthenticatedAnswer(t *testing.T) {
	w, requests := process05QuestionFixture(t)
	cfg := bindWorkspaceMCP(t, w)
	log, _ := fakeCodexDecisionPane(t, "pasted")
	w.registerPrivateProcess05Questions(t.Context(), requests[:1])
	id, p := pendingCodexDecision(t, w)
	completed := make(chan bool, 1)
	go func() {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			data, _ := os.ReadFile(log)
			if strings.Contains(string(data), "paste-buffer") {
				submitMCP(t, cfg, fmt.Sprintf(`{"submission_id":"answered-before-submit","proposal":{"action":"decide","id":%q,"allow":false,"reason":"independent fixture denial"}}`, id))
				completed <- true
				return
			}
			time.Sleep(time.Millisecond)
		}
		completed <- false
	}()
	w.deliverCodexDecision(t.Context(), id, p)
	if !<-completed {
		t.Fatal("fixture did not answer during presentation")
	}
	events, err := w.store.ListEvents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Type == "PERMISSION_DECIDED" && event.Data["request_id"] == id {
			if event.Data["decision_reason"] != "independent fixture denial" || event.Data["allow"] != false {
				t.Fatal("presentation failure overwrote authenticated native judgment")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("authenticated answer not applied")
	}
}
