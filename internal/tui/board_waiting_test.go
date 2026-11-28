package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/board/vm"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	boardmodel "github.com/Zen1th53/marshal/internal/tui/board/model"
)

var waitNow = time.Date(2026, 11, 20, 19, 31, 0, 0, time.UTC)

// waitInput is the flow input for run with workers, approvals and a held
// launch, at waitNow (an injected clock: nothing sleeps).
func waitInput(run marshal.Run, workers []boardmodel.Worker, approvals []app.ApprovalNoticeView, hold *app.DispatchAdmission) flowIn {
	b := boardmodel.Project(boardmodel.Snapshot{RunID: "R-1", Run: run, Workers: workers, Now: waitNow}, nil)
	p := &MarshalPanel{RunID: "R-1", Provider: "claude", State: run.State, Board: b, run: run, approvals: approvals, hold: hold}
	return flowIn{now: waitNow, hasRun: true, panel: p, run: run, v: boardVisual{board: b, now: waitNow}}
}

func waitTask(id string, state marshal.TaskState, deps ...string) marshal.Task {
	return marshal.Task{PlanTaskID: id, Title: "task " + id, Worker: "claude", State: state, DependsOn: deps, Files: []string{"web/" + id + ".go"}}
}

func waitTaskRow(t *testing.T, rows []vm.Task, id string) vm.Task {
	t.Helper()
	for _, r := range rows {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("no task %s in %+v", id, rows)
	return vm.Task{}
}

// Each kind of wait has its own code: a dependency names the task it waits
// on, a held launch its admission cause and retry time, a parked task the
// owner, a review its queue or role, an accepted task its checks, an
// approval its authority; a lane says how its signal stands; a paused run
// says what resumes it.
func TestBoardExplainsEveryWaitingState(t *testing.T) {
	retry := waitNow.Add(5 * time.Minute)
	run := marshal.Run{State: marshal.Dispatching, PlanID: "PLAN-1", PlanVersion: 1, Tasks: []marshal.Task{
		waitTask("T01", marshal.Dispatched),
		waitTask("T02", marshal.Queued, "T01"),
		waitTask("T03", marshal.Queued),
		waitTask("T04", marshal.Escalated),
		waitTask("T05", marshal.HandedIn),
		waitTask("T06", marshal.Accepted),
		waitTask("T07", marshal.Dispatched),
		waitTask("T08", marshal.Merged),
		waitTask("T09", marshal.Queued),
	}}
	workers := []boardmodel.Worker{
		{ID: "claude-1", Provider: "claude", TaskID: "T01", Heartbeat: true, Started: waitNow.Add(-10 * time.Minute), Beat: waitNow.Add(-5 * time.Second),
			Liveness: boardmodel.WorkerLiveness{Process: app.ProcessLive, Turn: app.TurnWorking}},
		{ID: "claude-2", Provider: "claude", TaskID: "T07", Heartbeat: true, Started: waitNow.Add(-10 * time.Minute), Beat: waitNow.Add(-90 * time.Second),
			Liveness: boardmodel.WorkerLiveness{Process: app.ProcessUnverifiable, Turn: app.TurnUnknown, Note: "the process runs in another namespace"}},
	}
	expires := waitNow.Add(10 * time.Minute)
	approvals := []app.ApprovalNoticeView{{ApprovalID: "ap-9", TaskID: "T09", Authority: "owner", CreatedAt: waitNow.Add(-2 * time.Minute), ExpiresAt: &expires,
		Status: execution.ApprovalRequested, Command: "/approve execution:ap-9", Destination: "Owner (/approve)"}}
	hold := &app.DispatchAdmission{Code: app.AdmissionCapacity, Reason: "every lane new work may use is taken", TaskID: "T03", RetryAt: &retry}
	in := waitInput(run, workers, approvals, hold)
	tasks := flowTasks(in)

	cases := []struct {
		id, code, blocker string
		retry             time.Time
	}{
		{"T02", vm.WaitDependency, "T01", time.Time{}},
		{"T03", vm.WaitCapacity, "", retry},
		{"T04", vm.WaitOwner, "", time.Time{}},
		{"T05", vm.WaitReviewQueue, "", time.Time{}},
		{"T06", vm.WaitVerification, "", time.Time{}},
		{"T07", vm.WaitSignal, "claude-2", time.Time{}},
		{"T09", vm.WaitAuthority, "ap-9", time.Time{}},
	}
	for _, c := range cases {
		w := waitTaskRow(t, tasks, c.id).Waiting
		if w == nil || w.Code != c.code || w.BlockerID != c.blocker || !w.RetryAt.Equal(c.retry) || w.Label == "" {
			t.Errorf("%s waits %+v, want code %s blocker %q retry %s", c.id, w, c.code, c.blocker, c.retry)
		}
	}
	for _, id := range []string{"T01", "T08"} {
		if w := waitTaskRow(t, tasks, id).Waiting; w != nil {
			t.Errorf("%s moves but waits %+v", id, w)
		}
	}

	// The owner accepts tasks: a handed-in task waits on that role.
	run.Settings.AcceptanceMode = marshal.AcceptUser
	if w := waitTaskRow(t, flowTasks(waitInput(run, workers, nil, nil)), "T05").Waiting; w == nil || w.Code != vm.WaitReviewRole {
		t.Errorf("T05 with owner acceptance waits %+v", w)
	}
	run.Settings.AcceptanceMode = ""

	// Every admission cause maps to its own code.
	for code, want := range map[app.DispatchAdmissionCode]string{
		app.AdmissionScopeOverlap: vm.WaitOverlap, app.AdmissionScopeBroad: vm.WaitOverlap,
		app.AdmissionDependencyUnmerged: vm.WaitDependency, app.AdmissionCapacity: vm.WaitCapacity,
		app.AdmissionQuotaUnknown: vm.WaitProvider, app.AdmissionRecoveryRamp: vm.WaitProvider,
		app.AdmissionSerialPlan: vm.WaitAdmission, app.AdmissionStandardTier: vm.WaitAdmission, app.AdmissionStaleBase: vm.WaitAdmission,
	} {
		h := &app.DispatchAdmission{Code: code, Reason: string(code), TaskID: "T03", BlockingTaskIDs: []string{"T01"}}
		if w := waitTaskRow(t, flowTasks(waitInput(run, workers, nil, h)), "T03").Waiting; w == nil || w.Code != want || w.Source != "admission" {
			t.Errorf("hold %s: T03 waits %+v, want %s", code, w, want)
		}
	}

	// Lanes: a live signal, and one Marshal cannot verify (never exited).
	lanes := flowLanes(in).Items
	if len(lanes) != 2 {
		t.Fatalf("lanes = %+v", lanes)
	}
	if s := lanes[0].Signal; s.State != vm.SignalLive || !s.At.Equal(waitNow.Add(-5*time.Second)) || lanes[0].Waiting != nil {
		t.Errorf("live lane = %+v / %+v", s, lanes[0].Waiting)
	}
	if s := lanes[1].Signal; s.State != vm.SignalUnverifiable || !strings.HasPrefix(s.Label, "cannot verify") || lanes[1].Waiting == nil || lanes[1].Waiting.Code != vm.WaitSignal {
		t.Errorf("unverifiable lane = %+v / %+v", s, lanes[1].Waiting)
	}
	// Quiet past a minute, then a heartbeat lost: amber, then stale.
	quiet := []boardmodel.Worker{{ID: "claude-1", Provider: "claude", TaskID: "T01", Heartbeat: true, Beat: waitNow.Add(-90 * time.Second), Liveness: boardmodel.WorkerLiveness{Process: app.ProcessLive}}}
	if s := flowLanes(waitInput(run, quiet, nil, nil)).Items[0].Signal; s.State != vm.SignalQuiet {
		t.Errorf("quiet lane = %+v", s)
	}
	quiet[0].Beat = waitNow.Add(-boardmodel.WorkerBeatGrace - time.Minute)
	if l := flowLanes(waitInput(run, quiet, nil, nil)).Items[0]; l.Signal.State != vm.SignalStale || !strings.Contains(l.Signal.Label, "cannot verify") || l.Waiting == nil || l.Waiting.Code != vm.WaitSignal {
		t.Errorf("lost lane = %+v", l)
	}
	// A worker that waits long on its model waits on its provider.
	model := []boardmodel.Worker{{ID: "claude-1", Provider: "claude", TaskID: "T01", Heartbeat: true, Beat: waitNow.Add(-5 * time.Second),
		Liveness: boardmodel.WorkerLiveness{Process: app.ProcessLive, ModelWaitSince: waitNow.Add(-app.ModelWaitNoticeAfter - time.Minute)}}}
	if l := flowLanes(waitInput(run, model, nil, nil)).Items[0]; l.Waiting == nil || l.Waiting.Code != vm.WaitProvider || l.Waiting.BlockerID != "claude" {
		t.Errorf("model wait lane = %+v", l.Waiting)
	}

	// The run: a held launch, then a pause that names what resumes it.
	if w := runWaiting(in); w == nil || w.Code != vm.WaitCapacity {
		t.Errorf("held run waits %+v", w)
	}
	paused := run
	paused.State = marshal.AwaitingUser
	paused.Pause = &marshal.Pause{Reason: "task lease lost", Code: marshal.CodeInterruptedOperation, Action: marshal.PauseRetryTask, TaskID: "T01", Detail: "lease lost"}
	if w := runWaiting(waitInput(paused, nil, nil, nil)); w == nil || w.Code != vm.WaitLease || w.BlockerID != "T01" || !strings.Contains(w.Inspect, "/marshal") {
		t.Errorf("paused run waits %+v", w)
	}
	paused.Pause = &marshal.Pause{Reason: "every lane is held", Code: marshal.CodeLaneAdmission, Action: marshal.PauseResume}
	if w := runWaiting(waitInput(paused, nil, nil, nil)); w == nil || w.Code != vm.WaitAdmission {
		t.Errorf("admission pause waits %+v", w)
	}
}

// Every blocked task, lane and approval carries a known code (or unknown)
// with where it was read and where to look or answer; approvals carry their
// age, the owner's bound answers only while they can be answered, and the
// Marshal's own show where they are answered.
func TestAllBlockedEntitiesHaveWaitReasonAndDestination(t *testing.T) {
	expires := waitNow.Add(10 * time.Minute)
	gone := waitNow.Add(-time.Minute)
	run := marshal.Run{State: marshal.Dispatching, PlanID: "PLAN-1", PlanVersion: 1, Tasks: []marshal.Task{
		waitTask("T01", marshal.Dispatched), waitTask("T02", marshal.Queued, "T01"), waitTask("T03", marshal.Queued),
		waitTask("T04", marshal.Escalated), waitTask("T05", marshal.HandedIn), waitTask("T06", marshal.Accepted),
		waitTask("T07", marshal.Returned), waitTask("T08", marshal.Queued),
	}}
	workers := []boardmodel.Worker{{ID: "claude-1", Provider: "claude", TaskID: "T01", Heartbeat: true, Beat: waitNow.Add(-boardmodel.WorkerBeatGrace - time.Minute)}}
	approvals := []app.ApprovalNoticeView{
		{ApprovalID: "ap-1", TaskID: "T08", Authority: "owner", CreatedAt: waitNow.Add(-4 * time.Minute), ExpiresAt: &expires, Status: execution.ApprovalRequested,
			Command: "/approve execution:ap-1", Destination: "Owner (/approve)", OperationType: "git_push", TargetResource: "origin"},
		{ApprovalID: "ap-2", TaskID: "T07", Authority: "marshal", CreatedAt: waitNow.Add(-3 * time.Minute), Status: execution.ApprovalRequested,
			Command: "marshal_approve ap-2", Destination: "Marshal chat"},
		{ApprovalID: "ap-3", TaskID: "T03", Authority: "owner", CreatedAt: waitNow.Add(-30 * time.Minute), ExpiresAt: &gone, Status: execution.ApprovalRequested, Command: "/approve execution:ap-3"},
		{ApprovalID: "ap-4", TaskID: "T03", Authority: "owner", CreatedAt: waitNow.Add(-20 * time.Minute), Status: execution.ApprovalRequested, ReplacementID: "ap-5", Command: "/approve execution:ap-4"},
	}
	in := waitInput(run, workers, approvals, nil)
	for _, row := range flowTasks(in) {
		if row.State == "merged" || row.State == "cancelled" {
			continue
		}
		if row.State == "building" && row.Waiting == nil {
			continue // builds unblocked
		}
		w := row.Waiting
		if w == nil || w.Code == "" || w.Label == "" || w.Source == "" || w.Inspect == "" {
			t.Errorf("task %s (%s) waits %+v: want code, label, source and where to look", row.ID, row.State, w)
		}
	}
	if w := waitTaskRow(t, flowTasks(in), "T03").Waiting; w == nil || w.Code != vm.WaitUnknown || w.Source == "" || w.Inspect == "" {
		t.Errorf("T03 with no known cause waits %+v, want unknown with source and where to look", w)
	}
	for _, l := range flowLanes(in).Items {
		if l.Waiting == nil || l.Waiting.Code != vm.WaitSignal || l.Waiting.Inspect == "" || l.Signal.State != vm.SignalStale {
			t.Errorf("lost lane %s = %+v / %+v", l.Name, l.Signal, l.Waiting)
		}
	}

	got := flowApprovals(in)
	if len(got) != 4 {
		t.Fatalf("approvals = %+v", got)
	}
	byID := map[string]vm.Approval{}
	for _, a := range got {
		byID[a.ID] = a
		if a.Waiting == nil || a.Waiting.Code == "" || a.Waiting.Source == "" || a.Waiting.Inspect == "" || a.CreatedAt.IsZero() {
			t.Errorf("approval %s = %+v", a.ID, a)
		}
	}
	if a := byID["ap-1"]; a.Status != "requested" || a.Awaiting != "owner" || len(a.Actions) != 2 || a.Actions[0].Key != "/approve execution:ap-1" ||
		!a.ExpiresAt.Equal(expires) || a.Waiting.Code != vm.WaitAuthority || a.Reason != "git_push · origin" {
		t.Errorf("owner approval = %+v", a)
	}
	if a := byID["ap-2"]; a.Awaiting != "marshal" || len(a.Actions) != 0 || a.Destination != "Marshal chat" || a.Waiting.Code != vm.WaitAuthority {
		t.Errorf("Marshal approval = %+v", a)
	}
	if a := byID["ap-3"]; a.Status != "expired" || len(a.Actions) != 0 || a.Waiting.Code != vm.WaitExpiry {
		t.Errorf("expired approval = %+v", a)
	}
	if a := byID["ap-4"]; a.Status != "superseded" || len(a.Actions) != 0 || a.Waiting.BlockerID != "ap-5" {
		t.Errorf("superseded approval = %+v", a)
	}
	// An expired or superseded approval holds no task: T03 is back to an
	// unknown wait, T08 waits on its owner approval, T07 on the Marshal's.
	if w := waitTaskRow(t, flowTasks(in), "T08").Waiting; w == nil || w.BlockerID != "ap-1" || w.Inspect != "/approve execution:ap-1" {
		t.Errorf("T08 waits %+v", w)
	}
	if w := waitTaskRow(t, flowTasks(in), "T07").Waiting; w == nil || w.BlockerID != "ap-2" || !strings.Contains(w.Label, "Marshal") {
		t.Errorf("T07 waits %+v", w)
	}
	// The age progresses with the injected clock, not a sleep.
	later := in
	later.now = expires.Add(time.Second)
	if a := flowApprovals(later)[0]; a.Status != "expired" || len(a.Actions) != 0 {
		t.Errorf("ap-1 after its expiry = %+v", a)
	}
}
