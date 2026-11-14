//go:build !governed

package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
)

// The person pauses the run while task a's lane has not started its turn and
// task b's lane is mid-turn. The pause never breaks the run: a goes back to
// waiting with no rework, b finishes its turn and is collected, the Marshal
// holds dispatch until the person resumes, then continues a and the run
// completes.
func TestExtremePauseDuringLanesNeverBreaksRun(t *testing.T) {
	t.Run("held dispatch", pauseHeldDispatchNeverBreaksRun)
	// The released v0.2.12 run: the person paused the run while a lane was
	// mid-turn and resumed it while that lane was still mid-turn. Through
	// the real Process05 lanes the run must still pass required completion
	// validation and deliver.
	t.Run("process05 lane resumed mid-turn", func(t *testing.T) { pausedProcess05LaneRun(t, resumeMidTurn) })
	t.Run("process05 lane resumed after its turn", func(t *testing.T) { pausedProcess05LaneRun(t, resumeAfterTurn) })
	t.Run("process05 lane never resumed", func(t *testing.T) { pausedProcess05LaneRun(t, neverResume) })
	// A run v0.2.12 left stopped this way continues on Enter.
	t.Run("process05 stuck run continues", func(t *testing.T) { pausedProcess05LaneRun(t, stuckThenContinue) })
}

func pauseHeldDispatchNeverBreaksRun(t *testing.T) {
	s, _ := setupExtremeService(t, 2)
	fake := s.Model.(marshalFakeModel)
	draft := fake.draft
	for i := range draft.Tasks {
		draft.Tasks[i].DependsOn = nil
		draft.Plan.Tasks[i].DependsOn = nil
	}
	graph, err := plan.BuildGraph(draft.Plan.Tasks)
	if err != nil {
		t.Fatal(err)
	}
	draft.Plan.Graph = graph
	s.Model = marshalFakeModel{draft: draft, review: fake.review}

	var mu sync.Mutex
	held := false
	changed := make(chan struct{})
	launches := map[string]int{}
	started := make(chan string, 4)
	paused := make(chan struct{})
	s.HeldTask = func(string, string) bool { mu.Lock(); defer mu.Unlock(); return held }
	s.HoldChanged = func() <-chan struct{} { mu.Lock(); defer mu.Unlock(); return changed }
	s.Drivers["worker"] = driver.Governed{Provider: "test", Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		id := req.Task.PlanTaskID
		mu.Lock()
		launches[id]++
		first := launches[id] == 1
		mu.Unlock()
		started <- id
		if first {
			<-paused
			if id == "a" {
				// a's lane was paused before its turn began.
				return nil, ErrProcess05Paused
			}
			// b was mid-turn: its turn finishes despite the pause.
		}
		return nil, os.WriteFile(filepath.Join(req.Worktree, id+".txt"), []byte("done\n"), 0600)
	}}
	if _, err := s.StartPlanning(t.Context(), "par", "write", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(t.Context(), "par"); err != nil {
		t.Fatal(err)
	}
	run, rev, err := s.load(t.Context(), "par")
	if err != nil {
		t.Fatal(err)
	}
	run.Process05Bound = true
	bindPrivateProcess05Tier(&run, plan.ExecutionPlan{Mode: plan.ModeUltra})
	run.Settings.GlobalCap = 2
	if err := s.save(t.Context(), "par", run, rev); err != nil {
		t.Fatal(err)
	}

	type result struct {
		run marshal.Run
		err error
	}
	done := make(chan result, 1)
	go func() {
		r, err := s.Execute(t.Context(), "par", func(marshal.Task, BriefContext) string { return "brief" }, nil)
		done <- result{r, err}
	}()
	for range 2 {
		select {
		case <-started:
		case <-time.After(10 * time.Second):
			t.Fatal("both lanes did not start")
		}
	}
	// The person pauses: the TUI holds dispatch, and the lanes stop.
	mu.Lock()
	held = true
	mu.Unlock()
	close(paused)

	// a goes back to waiting and b is collected; nothing new launches.
	deadline := time.Now().Add(10 * time.Second)
	for {
		now, _, err := s.load(t.Context(), "par")
		if err != nil {
			t.Fatal(err)
		}
		a, b := now.Tasks[0], now.Tasks[1]
		if a.State == marshal.Queued && (b.State == marshal.Accepted || b.State == marshal.Merged) {
			if len(a.ReturnsByAgent) != 0 {
				t.Fatalf("the pause counted as rework: %+v", a)
			}
			break
		}
		select {
		case r := <-done:
			t.Fatalf("the run ended while paused: %s %v", r.run.State, r.err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("paused tasks did not settle: a=%s b=%s", a.State, b.State)
		}
		time.Sleep(20 * time.Millisecond)
	}
	select {
	case id := <-started:
		t.Fatalf("task %s launched while the run was paused", id)
	case <-time.After(300 * time.Millisecond):
	}

	// The person resumes: the hold is released and a continues.
	mu.Lock()
	held = false
	close(changed)
	changed = make(chan struct{})
	mu.Unlock()
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("the run failed after resume: %v", r.err)
		}
		for _, task := range r.run.Tasks {
			if task.State != marshal.Merged || len(task.ReturnsByAgent) != 0 {
				t.Fatalf("task %s ended %s (returns %v) after resume", task.PlanTaskID, task.State, task.ReturnsByAgent)
			}
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the run did not complete after resume")
	}
	mu.Lock()
	defer mu.Unlock()
	if launches["a"] != 2 || launches["b"] != 1 {
		t.Fatalf("launches %v, want a twice (paused, then continued) and b once", launches)
	}
}

// pausedLaneResume is when the person resumes a run paused while a lane was
// mid-turn.
type pausedLaneResume int

const (
	// resumeMidTurn resumes all while the lane is still mid-turn.
	resumeMidTurn pausedLaneResume = iota
	// resumeAfterTurn resumes all once the lane's turn has finished.
	resumeAfterTurn
	// neverResume leaves the run paused while everything finishes; the run
	// must wait, not stop, and only continues when the person resumes.
	neverResume
	// stuckThenContinue resumes mid-turn on a runtime that does not settle
	// the paused lane (v0.2.12): the final check stops on it. Enter
	// (/marshal resume) on the fixed runtime then finishes the run.
	stuckThenContinue
)

// pausedProcess05LaneRun runs two ULTRA tasks through the real Process05
// lanes (canonical Engine, owned fixture provider, canonical verifier) and
// pauses the run, the way the palette's "Pause all" does, while task
// convert's lane is mid-turn. "Resume all" resumes what v0.2.12 resumed: the
// root, and a lane only once its task finished (an unfinished lane is left
// for the Marshal to continue). Whatever the timing, every lane must end
// done pending verification, the run must pass required completion
// validation, and close delivers it.
func pausedProcess05LaneRun(t *testing.T, when pausedLaneResume) {
	ctx := context.Background()
	fixture, _ := marshalFixture(t, 1)
	runtime := requiredProcess05TestRuntime(t, fixture)
	ctx = requiredTestOwnerContext(t, runtime, ctx)
	projectRecord, err := runtime.Store().Project(ctx)
	if err != nil {
		t.Fatal(err)
	}
	projectID := requiredProcess05TestProjectID(runtime, projectRecord.ID)
	goal := planGoal()
	goal.ID, goal.SessionID, goal.ProjectID = "GOAL-p05-pause", "SESSION-p05-pause", projectID
	goal.SuccessCriteria = []string{"convert.txt exists", "tests.txt exists"}
	if err = runtime.Store().SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatal(err)
	}
	request := requiredProcess05PlanRequest(planCreateRequest())
	request.Mode = plan.ModeUltra
	request.SessionID, request.ProjectID = goal.SessionID, projectid.ID(projectID)
	request.Tasks = []plan.Task{
		{ID: "convert", Title: "convert the notes", Mutating: true, Weight: 1, Paths: []string{"convert.txt"}, Criteria: []string{"convert.txt exists"}, Instructions: "Write convert.txt"},
		{ID: "tests", Title: "add the tests", Mutating: true, Weight: 1, Paths: []string{"tests.txt"}, Criteria: []string{"tests.txt exists"}, Instructions: "Write tests.txt"},
	}
	p, err := runtime.Plans().Create(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	p.Checks = map[string][]string{"convert": {"test -f convert.txt"}, "tests": {"test -f tests.txt"}}
	if err = runtime.Store().SavePlan(ctx, p, p.Version); err != nil {
		t.Fatal(err)
	}
	if _, err = runtime.Plans().Approve(ctx, projectid.ID(projectID)); err != nil {
		t.Fatal(err)
	}
	service := runtime.Execution()
	engine := service.Engine()
	midTurn := make(chan struct{})
	finishTurn := make(chan struct{})
	var turns sync.Map
	requiredRegisterProcess05TestHarness(t, runtime, "codex", func(_ context.Context, task execution.TaskExecution, _ execution.ConstraintPackage, worktree string) (execution.TaskResult, error) {
		count, _ := turns.LoadOrStore(task.TaskID, new(atomic.Int32))
		count.(*atomic.Int32).Add(1)
		trap, err := runtime.armHoneypot(worktree)
		if err != nil {
			return execution.TaskResult{}, err
		}
		t.Cleanup(func() { _ = trap.Close() })
		if task.TaskID == "convert" {
			close(midTurn)
			<-finishTurn
		}
		if err := os.WriteFile(filepath.Join(worktree, task.TaskID+".txt"), []byte("done\n"), 0600); err != nil {
			return execution.TaskResult{}, err
		}
		return execution.TaskResult{TaskID: task.TaskID, Success: true}, nil
	})
	s, err := runtime.MarshalWired(MarshalWiring{Provider: "codex", Approver: func(context.Context, string, string) (string, error) { return "operator", nil }})
	if err != nil {
		t.Fatal(err)
	}
	s.Gate = ultraGate{enabled: true}
	s.GateState = fixture.GateState
	s.ModelProvider = "review-test"
	s.Model = marshalFakeModel{review: marshal.Review{Verdict: marshal.VerdictAccept, Reviewer: "marshal"}}
	s.GovernedCheck = fixtureCheckRunner
	s.InstalledVersion = func(context.Context, string) string { return "1.0" }
	if err = s.Store.SaveHarnessProfile(ctx, model.HarnessProfile{Harness: "codex", InstalledVersion: "1.0", ProbeEvidenceID: "fixture-probe", ProbedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	s.GovernedDrivers = map[string]driver.Driver{"codex": driver.Governed{Provider: "codex", Run: runtime.marshalProcess05Run(s)}}
	s.CrossReview = func(_ context.Context, task marshal.Task, _ marshal.HandIn, _ marshal.Control) (marshal.Review, string, error) {
		return marshal.Review{Verdict: marshal.VerdictAccept, Reviewer: "second", EvidenceRefs: []string{"check:test -f " + task.PlanTaskID + ".txt"}}, "review-test", nil
	}
	s.Verify = fixture.Verify
	s.VerifierProvider = func(context.Context, marshal.Run) (string, error) { return "verifier-test", nil }
	if s.requiredIntegrationSource == nil || s.requiredCanonicalVerification == nil {
		t.Fatal("the shipped edition's required completion validation is not wired")
	}
	// The TUI's pause hold (commands_runcontrol.go): while held, the Marshal
	// launches nothing new.
	var mu sync.Mutex
	held := false
	changed := make(chan struct{})
	s.HeldTask = func(string, string) bool { mu.Lock(); defer mu.Unlock(); return held }
	s.HoldChanged = func() <-chan struct{} { mu.Lock(); defer mu.Unlock(); return changed }
	hold := func(on bool) {
		mu.Lock()
		defer mu.Unlock()
		held = on
		close(changed)
		changed = make(chan struct{})
	}
	const id = "p05-pause"
	if _, err = s.BindApprovedPlan(ctx, id); err != nil {
		t.Fatal(err)
	}
	brief := func(marshal.Task, BriefContext) string { return "do the approved task" }
	settle := s.process05Settle
	if settle == nil || s.process05Paused == nil {
		t.Fatal("the runtime does not settle a finished paused lane")
	}
	if when == stuckThenContinue {
		s.process05Settle = nil
	}
	// Each lane asks for its own task approval first; the operator grants
	// every one, then the run continues.
	execute := func(ctx context.Context) (marshal.Run, error) {
		for {
			run, err := s.Execute(ctx, id, brief, nil)
			if err != nil || run.State != marshal.AwaitingUser || run.Pause == nil || run.Pause.Code != marshal.CodeApprovalWait {
				return run, err
			}
			runs, err := service.ListRuns(ctx)
			if err != nil {
				return run, err
			}
			for _, lane := range runs {
				if task := lane.Tasks[lane.LaneTaskID]; lane.ParentRunID != "" && task.State == execution.TaskNeedsApproval {
					if err = service.Approve(ctx, task.ApprovalID, "operator", "approve the exact task"); err != nil {
						return run, err
					}
				}
			}
			if _, err = s.Resume(ctx, id); err != nil {
				return run, err
			}
		}
	}

	type result struct {
		run marshal.Run
		err error
	}
	execCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan result, 1)
	go func() {
		r, err := execute(execCtx)
		done <- result{r, err}
	}()
	select {
	case <-midTurn:
	case r := <-done:
		t.Fatalf("the run ended before convert's turn: %s %v", r.run.State, r.err)
	case <-time.After(20 * time.Second):
		t.Fatal("convert's turn did not start")
	}
	runsNow := func() (execution.ExecutionRun, map[string]execution.ExecutionRun) {
		t.Helper()
		all, err := service.ListRuns(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var root execution.ExecutionRun
		lanes := map[string]execution.ExecutionRun{}
		for _, run := range all {
			if run.ParentRunID == "" && len(run.TaskLanes) > 0 {
				root = run
			}
		}
		for task, laneID := range root.TaskLanes {
			for _, run := range all {
				if run.RunID == laneID {
					lanes[task] = run
				}
			}
		}
		return root, lanes
	}
	control := func(runID, operation string) {
		t.Helper()
		for range 50 {
			current, err := service.GetRun(ctx, runID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = engine.ControlRun(ctx, runID, operation, current.Version); err == nil {
				return
			} else if !errors.Is(err, execution.ErrRunConflict) {
				t.Fatalf("%s %s: %v", operation, runID, err)
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("%s %s kept conflicting", operation, runID)
	}
	if when == neverResume {
		// Here task tests finishes before the pause, so every task is done
		// while the person keeps the run paused. In the other cases tests
		// has not started its turn: the pause parks it and the Marshal
		// dispatches it again after resume.
		deadline := time.Now().Add(20 * time.Second)
		for {
			now, _, err := s.load(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if now.Tasks[taskIndex(now, "tests")].State == marshal.Accepted {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("task tests was not accepted: %s", now.Tasks[taskIndex(now, "tests")].State)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	// "Pause all": the TUI holds dispatch and pauses the run; the root
	// pauses its running lanes.
	hold(true)
	root, _ := runsNow()
	control(root.RunID, "pause")
	if _, lanes := runsNow(); lanes["convert"].State != execution.RunPaused {
		t.Fatalf("convert's lane did not pause mid-turn: %s", lanes["convert"].State)
	}
	// "Resume all" as v0.2.12 did it: the root, and each paused lane whose
	// task finished; an unfinished lane is left for the Marshal.
	resumeAll := func() {
		t.Helper()
		root, lanes := runsNow()
		if root.State == execution.RunPaused {
			control(root.RunID, "resume")
		}
		for task, lane := range lanes {
			if state := lane.Tasks[task].State; lane.State == execution.RunPaused && (state == execution.TaskCompletedPendingVerify || state.IsTerminal()) {
				control(lane.RunID, "resume")
			}
		}
		hold(false)
	}
	convertFinished := func() {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for {
			now, _, err := s.load(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if state := now.Tasks[taskIndex(now, "convert")].State; state != marshal.Dispatched && state != marshal.Queued {
				return
			}
			select {
			case r := <-done:
				t.Fatalf("the run ended while convert finished: %s %v", r.run.State, r.err)
			default:
			}
			if time.Now().After(deadline) {
				t.Fatal("convert's turn was not collected")
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	switch when {
	case resumeMidTurn, stuckThenContinue:
		resumeAll()
		close(finishTurn)
	case resumeAfterTurn:
		close(finishTurn)
		convertFinished()
		resumeAll()
	case neverResume:
		close(finishTurn)
		convertFinished()
		// Every task finishes while the person keeps the run paused: the
		// run waits as paused. It neither stops on a verification error
		// nor settles the paused lane behind the person's back.
		select {
		case r := <-done:
			t.Fatalf("a paused run did not wait: %s %+v %v", r.run.State, r.run.Pause, r.err)
		case <-time.After(1500 * time.Millisecond):
		}
		now, _, err := s.load(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if now.State == marshal.AwaitingUser || now.Pause != nil {
			t.Fatalf("a paused run stopped instead of waiting: %+v", now.Pause)
		}
		if _, lanes := runsNow(); lanes["convert"].State != execution.RunPaused {
			t.Fatalf("the paused lane moved without the person: %s", lanes["convert"].State)
		}
		resumeAll()
	}
	if when == stuckThenContinue {
		var stuck result
		select {
		case stuck = <-done:
		case <-time.After(60 * time.Second):
			t.Fatal("the run did not reach its final check")
		}
		if !errors.Is(stuck.err, execution.ErrRunConflict) {
			t.Fatalf("v0.2.12 did not stop on the paused lane: %v", stuck.err)
		}
		pause := stuck.run.Pause
		if stuck.run.State != marshal.AwaitingUser || pause == nil || pause.Code != marshal.CodeVerificationStopped || pause.ResumeState != marshal.Verifying {
			t.Fatalf("stop: %s %+v", stuck.run.State, pause)
		}
		// The person reads plain words; the raw error stays in the detail
		// and the recorded event.
		if pause.Reason != "Marshal could not finish checking the run (part of it was left paused)." || len(pause.Resolutions) != 1 || pause.Resolutions[0] != "Press Enter to let the Marshal retry." || !strings.Contains(pause.Detail, "required completion child identity/state") {
			t.Fatalf("stop words: %+v", pause)
		}
		decisions, err := s.Store.MarshalDecisions(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		recorded := false
		for _, ev := range decisions {
			if ev.Type == events.EventTypeMarshalEscalated && ev.Data["summary"] == pause.Reason && strings.Contains(fmt.Sprint(ev.Data["reason"]), "required completion child identity/state") {
				recorded = true
			}
		}
		if !recorded {
			t.Fatal("the stop's event lost the plain summary or the raw reason")
		}
		if _, lanes := runsNow(); lanes["convert"].State != execution.RunPaused || lanes["convert"].Tasks["convert"].State != execution.TaskCompletedPendingVerify {
			t.Fatalf("not the v0.2.12 state: lane %s task %s", lanes["convert"].State, lanes["convert"].Tasks["convert"].State)
		}
		// Enter on the fixed runtime: /marshal resume, then the run goes on.
		s.process05Settle = settle
		if _, err := s.Resume(ctx, id); err != nil {
			t.Fatal(err)
		}
		go func() {
			r, err := execute(execCtx)
			done <- result{r, err}
		}()
	}
	var finished marshal.Run
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("the run failed after the pause: %v (pause %+v)", r.err, r.run.Pause)
		}
		finished = r.run
	case <-time.After(60 * time.Second):
		t.Fatal("the run did not finish after resume")
	}
	if finished.State != marshal.Verifying || finished.Pause != nil {
		t.Fatalf("the run did not pass verification: %s %+v", finished.State, finished.Pause)
	}
	for _, task := range finished.Tasks {
		if task.State != marshal.Merged || len(task.ReturnsByAgent) != 0 {
			t.Fatalf("task %s ended %s (returns %v)", task.PlanTaskID, task.State, task.ReturnsByAgent)
		}
	}
	root, lanes := runsNow()
	if root.State != execution.RunDonePendingVerification || root.CurrentPhase != execution.PhaseCompletedPending || len(lanes) != 2 {
		t.Fatalf("root ended %s/%s with %d lanes", root.State, root.CurrentPhase, len(lanes))
	}
	for task, lane := range lanes {
		if lane.State != execution.RunDonePendingVerification || lane.CurrentPhase != execution.PhaseCompletedPending || lane.SelectedTask != "" || lane.Tasks[task].State != execution.TaskCompletedPendingVerify || root.TaskLaneStates[task] != execution.TaskCompletedPendingVerify {
			t.Fatalf("lane %s ended %s/%s selected=%q task=%s", task, lane.State, lane.CurrentPhase, lane.SelectedTask, lane.Tasks[task].State)
		}
	}
	for _, task := range []string{"convert", "tests"} {
		count, _ := turns.Load(task)
		if count == nil || count.(*atomic.Int32).Load() != 1 {
			t.Fatalf("task %s ran %v turns, want one", task, count)
		}
	}
	before := marshalGit(t, runtime.ProjectRoot(), "rev-parse", "refs/heads/main")
	marshalGit(t, runtime.ProjectRoot(), "checkout", "--detach")
	if err = s.Close(ctx, id); err != nil {
		t.Fatalf("delivery: %v", err)
	}
	closed, _, err := s.load(ctx, id)
	if err != nil || closed.State != marshal.Closed {
		t.Fatalf("close: %s %v", closed.State, err)
	}
	if marshalGit(t, runtime.ProjectRoot(), "rev-parse", "refs/heads/main") == before {
		t.Fatal("nothing was delivered to main")
	}
}
