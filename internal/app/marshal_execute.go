package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

// MarshalObserver receives the run after every step, so a surface can show
// progress without polling the store.
type MarshalObserver func(marshal.Run)

// BriefContext is what a worker's brief needs beyond its approved task.
type BriefContext struct {
	// Control is the run's approved control level.
	Control marshal.Control
	// Returned lists why earlier attempts at the task were sent back, oldest
	// first, so rework starts from the reasons instead of repeating them.
	Returned []string
	// Requirements, Index and Note come from the approved plan pack: what
	// the person asked for, how the tasks fit together, and this task's own
	// note. They are empty for a run drafted without a pack.
	Requirements, Index, Note string
	// ArchitectureContext is the accepted contract slice for this task. Brief
	// renderers carry it alongside Note, inside their own text or JSON format.
	ArchitectureContext string
	// Memory holds bounded project-memory recall relevant to the task.
	Memory []model.MemoryRecordV2
	// MemoryContext is the bounded, authorized Extreme worker memory contribution.
	MemoryContext string
	MemoryStatus  string
}

// MarshalBrief produces the instruction a worker receives for a task.
type MarshalBrief func(marshal.Task, BriefContext) string

// briefContext gathers durable return reasons, falling back to reviews for
// attempts recorded before return events carried their own reasons.
func (s *MarshalService) briefContext(ctx context.Context, runID string, run marshal.Run, t marshal.Task) (BriefContext, error) {
	return s.briefContextFromTask(ctx, runID, run, t, false)
}

func (s *MarshalService) briefContextFromTask(ctx context.Context, runID string, run marshal.Run, t marshal.Task, baseResolved bool) (BriefContext, error) {
	bc := BriefContext{Control: run.TaskControl(t)}
	if architecture, required := s.taskArchitecture(ctx, t); required {
		bc.ArchitectureContext = architecture
	}
	if run.Pack != nil {
		bc.Requirements, bc.Index, bc.Note = run.Pack.Requirements, run.Pack.Index, run.Pack.Tasks[t.PlanTaskID]
	}
	if err := s.addWorkerMemory(ctx, runID, run, t, baseResolved, &bc); err != nil {
		return bc, err
	}
	returned, err := s.taskReturnReasons(ctx, runID, t)
	if err != nil {
		return bc, err
	}
	bc.Returned = returned
	return bc, nil
}

// taskReturnReasons lists why earlier attempts at the task were returned,
// oldest first, from durable return events, falling back to reviews for
// attempts recorded before return events carried their own reasons.
func (s *MarshalService) taskReturnReasons(ctx context.Context, runID string, t marshal.Task) ([]string, error) {
	var out []string
	attempts := taskReturns(t)
	history, err := s.marshalDecisionsResolved(ctx, runID)
	if err != nil {
		return nil, err
	}
	returns := map[string][]string{}
	for _, event := range history {
		if event.TaskID != t.PlanTaskID || event.Data["return_attempt"] == nil {
			continue
		}
		data, err := json.Marshal(event.Data["return_reasons"])
		if err != nil {
			return nil, err
		}
		var reasons []string
		if err := json.Unmarshal(data, &reasons); err != nil {
			return nil, err
		}
		returns[fmt.Sprint(event.Data["return_attempt"])] = reasons
	}
	for attempt := 1 + t.EvidenceAttemptBase; attempt <= attempts+t.EvidenceAttemptBase; attempt++ {
		if reasons, ok := returns[fmt.Sprint(attempt)]; ok {
			out = append(out, reasons...)
			continue
		}
		review, err := s.Store.GetMarshalReview(ctx, runID, t.PlanTaskID, attempt)
		if errors.Is(err, model.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, review.Value.Reasons...)
		if review.Value.Independent != nil {
			out = append(out, review.Value.Independent.Reasons...)
		}
	}
	return out, nil
}

// Execute drives an approved run to the next point that needs a person:
// merge accepted work in plan order, review hand-ins, dispatch ready tasks
// under the tier policy, and verify the integrated result.
//
// Workers run in parallel: a round launches every ready task the tier
// allows before collecting any of them. Collection is sequential, which
// keeps each state change a single compare-and-swap write while the workers
// themselves still overlap.
//
// Execute returns when the run awaits the user, is closed, is ready for the
// user to close, or ctx ends. Under acceptance mode "marshal" with a valid
// standing close authorization it closes the run itself; the gate still
// decides.
func (s *MarshalService) Execute(ctx context.Context, runID string, brief MarshalBrief, observe MarshalObserver) (marshal.Run, error) {
	if brief == nil {
		return marshal.Run{}, fmt.Errorf("marshal execute: no brief for workers")
	}
	w := s.runWriter(runID)
	executionID, _ := model.NewID("execution-")
	if activeID, started := w.startExecution(executionID); !started {
		run, err := s.Snapshot(ctx, runID)
		if err == nil && activeID != "" {
			run.ExecutionID = activeID
		}
		return run, err
	}
	w.mu.Lock()
	defer func() { w.endExecution(); w.mu.Unlock() }()
	ctx = context.WithValue(ctx, runWriterContextKey{}, w)
	if run, rev, err := s.load(ctx, runID); err == nil {
		run.ExecutionID = executionID
		if err = s.save(ctx, runID, run, rev); err != nil {
			return run, err
		}
	} else {
		return run, err
	}
	current := func() (marshal.Run, error) {
		run, _, err := s.load(ctx, runID)
		if err == nil && observe != nil {
			observe(run)
		}
		return run, err
	}
	for round := 0; ; round++ {
		if err := s.runCommandSafePoint(ctx, runID); err != nil {
			return marshal.Run{}, err
		}
		run, err := current()
		if err != nil {
			return run, err
		}
		// Every round either changes a task's state or stops. The bound
		// includes recorded infrastructure failures, whose retries have
		// their own policy. Reaching it means something else loops; the
		// run is handed to the user instead of spending more budget.
		if round > marshalMaxRounds(run) {
			if err := s.escalateWithDetail(ctx, runID, "", marshal.CodeRunStalled, "the run stopped making progress", stalledRunDetail(run, round)); err != nil {
				return run, err
			}
			return current()
		}
		if err := ctx.Err(); err != nil {
			return run, err
		}
		switch run.State {
		case marshal.AwaitingUser, marshal.Closed, marshal.Drafting:
			return run, nil
		case marshal.Verifying:
			// The person paused part of the run and has not resumed it:
			// the run waits, as paused, before its final check.
			if waited, err := s.waitPausedExecution(ctx, runID, run); err != nil {
				return run, err
			} else if waited {
				round--
				continue
			}
			return s.finish(ctx, runID, current)
		}
		progressed, err := s.mergeAccepted(ctx, runID, run)
		if err != nil || progressed {
			if err != nil {
				return run, err
			}
			continue
		}
		progressed, err = s.reviewHandedIn(ctx, runID, run)
		if err != nil || progressed {
			if err != nil {
				return run, err
			}
			continue
		}
		// An approval that expired unanswered is cleared before dispatch,
		// so its task relaunches in this round beside its siblings instead
		// of waiting until nothing else can move.
		if cleared, err := s.clearExpiredProcess05WaitsBeforeDispatch(ctx, runID); err != nil {
			after, _ := current()
			return after, err
		} else if cleared {
			continue
		}
		launched, err := s.dispatchReady(ctx, runID, run, brief)
		s.markDispatchLive(runID, launched)
		if err != nil {
			// Workers already launched in this round are still running;
			// collect them so none is left without a hand-in.
			err = errors.Join(err, s.collectAdmitted(ctx, runID, launched, observe, current))
			after, _ := current()
			return after, err
		}
		if len(launched) == 0 {
			// An escalation or reassignment during dispatch changes the run;
			// the next round reports it. Otherwise nothing can move.
			if after, _, err := s.load(ctx, runID); err == nil && after.State != run.State {
				continue
			}
			// Tasks waiting for their own approvals were passed by; once
			// nothing else can move, the run waits for those decisions.
			if again, err := s.pausePrivateProcess05Waits(ctx, runID); err != nil {
				after, _ := current()
				return after, err
			} else if again {
				continue
			}
			// A task the owner's change holds can still run once the hold
			// is released: wait for that instead of ending the run. The
			// wait does not use up a round.
			if waited, err := s.waitHeldTasks(ctx, runID, run); err != nil {
				after, _ := current()
				return after, err
			} else if waited {
				round--
				continue
			}
			if adm, ok := s.admissionHold(runID); ok {
				return run, &AdmissionHoldError{RunID: runID, Admission: adm}
			}
			return run, fmt.Errorf("marshal run %s cannot progress: no task is ready, handed in or accepted", runID)
		}
		// The launched tasks are dispatched now: show them working while
		// their workers run, not as the queued tasks this round started from.
		if observe != nil {
			_, _ = current()
		}
		if err := s.collectAdmitted(ctx, runID, launched, observe, current); err != nil {
			after, _ := current()
			return after, err
		}
	}
}

// collectAdmitted uses the mode reserved before launch, even after a preference change.
func (s *MarshalService) collectAdmitted(ctx context.Context, runID string, launched []MarshalDispatch, observe MarshalObserver, current func() (marshal.Run, error)) error {
	if len(launched) > 0 && launched[0].Tier == marshal.Ultra {
		return s.collectAndReviewUltra(ctx, runID, launched, observe, current)
	}
	return s.collectAll(ctx, runID, launched, observe, current)
}

// collectAndReviewUltra collects and reviews each launched worker in ULTRA as soon
// as it is ready. State writes to the run and store are serialized.
func (s *MarshalService) collectAndReviewUltra(ctx context.Context, runID string, launched []MarshalDispatch, observe MarshalObserver, current func() (marshal.Run, error)) error {
	return s.collectBatchCommands(ctx, runID, launched, observe, current, true)
}

func (s *MarshalService) collectAll(ctx context.Context, runID string, launched []MarshalDispatch, observe MarshalObserver, current func() (marshal.Run, error)) error {
	return s.collectBatchCommands(ctx, runID, launched, observe, current, false)
}

// Only driver completion waits happen outside the writer. Collection, review
// and queued task effects all execute here in one ordered stream.
func (s *MarshalService) collectBatchCommands(ctx context.Context, runID string, launched []MarshalDispatch, observe MarshalObserver, current func() (marshal.Run, error), review bool) error {
	var errs []error
	pending := append([]MarshalDispatch(nil), launched...)
	for len(pending) > 0 {
		if err := s.runCommandSafePoint(ctx, runID); err != nil {
			errs = append(errs, err)
		}
		cases := []reflect.SelectCase{{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ctx.Done())}}
		w, _ := ctx.Value(runWriterContextKey{}).(*runWriter)
		var wake <-chan struct{}
		if w != nil {
			wake = w.wake
		}
		cases = append(cases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(wake)})
		count := len(pending)
		if !review {
			count = 1
		}
		immediate := -1
		for i := 0; i < count; i++ {
			if pending[i].Handle == nil || pending[i].Handle.Done() == nil {
				immediate = i
				break
			}
			cases = append(cases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(pending[i].Handle.Done())})
		}
		index := immediate
		if index < 0 {
			if w != nil {
				w.mu.Unlock()
			}
			chosen, _, _ := reflect.Select(cases)
			if w != nil {
				w.mu.Lock()
			}
			if chosen == 1 {
				continue
			}
			if chosen == 0 {
				index = 0
			} else {
				index = chosen - 2
			}
		}
		// A queued decision gets a safe point before the next collection write.
		if err := s.drainRunCommands(ctx, runID); err != nil {
			errs = append(errs, err)
		}
		d := pending[index]
		pending = append(pending[:index], pending[index+1:]...)
		_, err := s.CollectHandIn(ctx, runID, d)
		s.clearDispatchLive(runID, d.TaskID)
		if err != nil {
			errs = append(errs, fmt.Errorf("collect %s: %w", d.TaskID, err))
		} else if review {
			if err = s.reviewCollectedDispatch(ctx, runID, d.TaskID); err != nil {
				errs = append(errs, fmt.Errorf("review %s: %w", d.TaskID, err))
			}
		}
		if observe != nil {
			_, _ = current()
		}
	}
	if err := s.drainRunCommands(context.WithoutCancel(ctx), runID); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// mergeAccepted merges accepted tasks in plan order, stopping at the first
// task that is not yet accepted: merge order is plan order.
func (s *MarshalService) mergeAccepted(ctx context.Context, runID string, run marshal.Run) (bool, error) {
	progressed := false
	for _, t := range run.Tasks {
		if t.State == marshal.Merged || t.State == marshal.Cancelled {
			continue
		}
		if t.State != marshal.Accepted {
			break
		}
		if err := s.Merge(ctx, runID, t.PlanTaskID); err != nil {
			return progressed, err
		}
		progressed = true
	}
	return progressed, nil
}

// reviewHandedIn reviews every hand-in. The Marshal model's usage on a
// review is not measured, so it is charged as unknown, never as zero.
func (s *MarshalService) reviewHandedIn(ctx context.Context, runID string, run marshal.Run) (bool, error) {
	progressed := false
	for _, t := range run.Tasks {
		if t.State != marshal.HandedIn {
			continue
		}
		if _, err := s.Review(ctx, runID, t.PlanTaskID, unknownMarshalCharge()); err != nil {
			return progressed, err
		}
		progressed = true
	}
	return progressed, nil
}

// dispatchReady launches every task whose dependencies are merged, up to the
// concurrency the tier policy allows at this moment.
//
// A task marked for reassignment first moves to a different worker; with no
// other worker available it is escalated. A worker whose harness MARSHAL
// cannot show it governs is not launched at all: its work could not be
// accepted, so running it would only spend the budget. The task is escalated
// with the governance reasons instead.
func (s *MarshalService) dispatchReady(ctx context.Context, runID string, run marshal.Run, brief MarshalBrief) ([]MarshalDispatch, error) {
	var launched []MarshalDispatch
	var ready []marshal.Task
	for _, t := range run.Tasks {
		if t.State != marshal.Queued && t.State != marshal.Returned && t.State != marshal.Reassigned {
			continue
		}
		if !marshalDepsMerged(run, t) {
			continue
		}
		if waiting, err := s.privateProcess05WaitPending(ctx, run, t); err != nil {
			return nil, err
		} else if waiting {
			continue
		}
		ready = append(ready, t)
	}
	gate, release := reserveExecution(s.Gate)
	// Reserve the batch before launching any worker. The owner can acknowledge
	// off immediately afterwards; every reserved attempt retains this mode.
	var candidates []marshal.Task
	var reserved []MarshalDispatch
	for _, t := range ready {
		if t.State != marshal.Queued && t.State != marshal.Returned && t.State != marshal.Reassigned {
			continue
		}
		if !marshalDepsMerged(run, t) || (s.HeldTask != nil && s.HeldTask(runID, t.PlanTaskID)) {
			continue
		}
		can, stop := canLaunchCandidate(gate, run, t, reserved)
		if stop {
			break
		}
		if !can {
			continue
		}
		candidates = append(candidates, t)
		reserved = append(reserved, MarshalDispatch{TaskID: t.PlanTaskID})
		if shouldStopAfterLaunchGate(gate, run, t) {
			break
		}
	}
	release()
	ctx = context.WithValue(ctx, admissionGateKey{}, gate)
	// A launch held by a provider's recovery pace goes to the end of the
	// round with its retry time; on its next turn the round waits for that
	// time, then rechecks everything below before it launches. The round
	// keeps one wait at a time: the earliest held launch comes first.
	waitUntil := map[string]time.Time{}
	var held []DispatchAdmission
	defer func() { s.noteAdmissionHold(runID, held) }()
	for idx := 0; idx < len(candidates); idx++ {
		t := candidates[idx]
		admitAt := waitUntil[t.PlanTaskID]
		if !admitAt.IsZero() {
			if err := s.waitAdmission(ctx, admitAt); err != nil {
				return launched, err
			}
		}
		if err := s.runCommandSafePoint(ctx, runID); err != nil {
			return launched, err
		}
		if fresh, _, err := s.load(ctx, runID); err != nil {
			return launched, err
		} else if !slices.Equal(run.Commands, fresh.Commands) {
			// Keep the reserved round's admission snapshot unless an operator
			// command changed it. Dispatch itself updates persisted task states.
			run = fresh
		}
		if run.CommandFence != "" || run.State == marshal.AwaitingUser {
			break
		}
		if i := taskIndex(run, t.PlanTaskID); i >= 0 {
			t = run.Tasks[i]
		}
		canLaunch, stop := canLaunchCandidate(gate, run, t, launched)
		if stop {
			break
		}
		if !canLaunch {
			continue
		}
		if t.State != marshal.Queued && t.State != marshal.Returned && t.State != marshal.Reassigned {
			continue
		}
		// The owner's mid-run change holds this task: skip it so it does not
		// launch until the hold is released. A task already running is past
		// this point and is never held.
		if s.HeldTask != nil && s.HeldTask(runID, t.PlanTaskID) {
			continue
		}
		if !marshalDepsMerged(run, t) {
			continue
		}
		if waiting, err := s.privateProcess05WaitPending(ctx, run, t); err != nil {
			return launched, err
		} else if waiting {
			continue
		}
		// Only a worker that used up its returns is moved on here; a task
		// the operator already moved (its new worker has none) keeps it.
		if t.State == marshal.Reassigned && len(t.ReturnsByAgent) <= 1 && t.ReturnsByAgent[t.Worker] > 0 && !isProviderFailover(t) {
			next := s.otherWorkerFor(ctx, run.Settings, t.Worker, t.Mode)
			if next == "" {
				reason := fmt.Sprintf("no other worker supports %s mode for reassignment; operator intervention required", t.Mode)
				return launched, errors.Join(errors.New(reason), s.Escalate(ctx, runID, t.PlanTaskID, marshal.CodeNoReassignWorker, reason))
			}
			if err := s.Reassign(ctx, runID, t.PlanTaskID, next); err != nil {
				return launched, err
			}
			t.Worker = next
			if i := taskIndex(run, t.PlanTaskID); i >= 0 {
				run.Tasks[i].Worker = next
			}
			canLaunch, stop = canLaunchCandidate(gate, run, t, launched)
			if stop {
				break
			}
			if !canLaunch {
				continue
			}
		}
		admissionPolicy := s.projectSettingsSnapshot(ctx, run.Settings)
		if s.InstalledVersion != nil && admissionPolicy.ProviderEnabled(t.Worker) {
			if (t.Mode == marshal.Governed || t.Worker == "agy") && s.ProbeWorker != nil && !s.workerTaskGovernance(ctx, t).Governed() {
				if err := s.ProbeWorker(ctx, t.Worker); err != nil {
					return launched, fmt.Errorf("governed worker verification failed: %w", err)
				}
			}
			if g := s.workerTaskGovernance(ctx, t); !g.Governed() {
				return launched, s.Escalate(ctx, runID, t.PlanTaskID, marshal.CodeWorkerNotGoverned, fmt.Sprintf("worker %s is not governed (%s): %s", t.Worker, g.State, strings.Join(g.Reasons, " ")))
			}
		}
		// The hold may have landed since the check above: claim the
		// launch under the hold's own lock so the two cannot cross.
		end := func() {}
		if s.ClaimDispatch != nil {
			claimEnd, ok := s.ClaimDispatch(runID, t.PlanTaskID)
			if !ok {
				continue
			}
			if claimEnd != nil {
				end = claimEnd
			}
		}
		d, err := s.dispatchWithBrief(ctx, runID, t.PlanTaskID, "", brief)
		end()
		if err != nil {
			var blocked *DispatchAdmissionError
			if !errors.As(err, &blocked) {
				return launched, err
			}
			adm := blocked.Admission
			if adm.RetryAt != nil && adm.RetryAt.After(admitAt) {
				if admitAt.IsZero() && s.RecordActivity != nil {
					s.RecordActivity(firstNonEmpty(strings.TrimSpace(t.Title), t.PlanTaskID) + " is waiting: " + adm.Reason + ".")
				}
				waitUntil[t.PlanTaskID] = *adm.RetryAt
				candidates = append(candidates, t)
				continue
			}
			held = append(held, adm)
			continue
		}
		launched = append(launched, d)
		s.markDispatchLive(runID, []MarshalDispatch{d})
		if shouldStopAfterLaunchGate(gate, run, t) {
			break
		}
	}
	return launched, nil
}

// pauseHolds reports whether the person's pause (or change) holds any task
// of run.
func (s *MarshalService) pauseHolds(runID string, run marshal.Run) bool {
	if s.HeldTask == nil {
		return false
	}
	for _, t := range run.Tasks {
		if s.HeldTask(runID, t.PlanTaskID) {
			return true
		}
	}
	return false
}

// waitPausedExecution blocks, before the final check, while the person's
// pause holds the run and part of its canonical execution is paused, until
// the hold changes or ctx ends. Checking the run then would only find a
// paused part and stop on it; the run waits as paused instead. It reports
// whether it waited.
func (s *MarshalService) waitPausedExecution(ctx context.Context, runID string, run marshal.Run) (bool, error) {
	if s.HoldChanged == nil || s.process05Paused == nil {
		return false, nil
	}
	changed := s.HoldChanged()
	if !s.pauseHolds(runID, run) || !s.process05Paused(ctx, run) {
		return false, nil
	}
	return s.waitRunChange(ctx, runID, changed)
}

// settleFinishedExecution ends each part of the run's canonical execution
// whose work finished while it was paused (Runtime.settleProcess05), right
// before the final check, which needs every part ended. Execute reaches it
// only once the person's pause no longer holds the run
// (waitPausedExecution); the person's own close may reach it during a hold.
func (s *MarshalService) settleFinishedExecution(ctx context.Context, run marshal.Run) error {
	if s.process05Settle == nil {
		return nil
	}
	return s.process05Settle(ctx, run)
}

// waitHeldTasks blocks while nothing else can move and a held task that has
// not started would otherwise be ready (its dependencies are merged), until
// the held set changes or ctx ends: only the hold stops the run, so it waits
// rather than ends. A run stuck for any other reason still reports it. It
// reports whether it waited, so Execute takes another round with the new
// hold. The change channel is taken before the held check, so a release in
// between is not missed.
func (s *MarshalService) waitHeldTasks(ctx context.Context, runID string, run marshal.Run) (bool, error) {
	if s.HeldTask == nil || s.HoldChanged == nil {
		return false, nil
	}
	changed := s.HoldChanged()
	held := false
	for _, t := range run.Tasks {
		if (t.State == marshal.Queued || t.State == marshal.Returned || t.State == marshal.Reassigned) && marshalDepsMerged(run, t) && s.HeldTask(runID, t.PlanTaskID) {
			held = true
			break
		}
	}
	if !held {
		return false, nil
	}
	return s.waitRunChange(ctx, runID, changed)
}

// finish verifies the integrated result and, when the user granted a
// standing close authorization, closes the run.
func (s *MarshalService) finish(ctx context.Context, runID string, current func() (marshal.Run, error)) (marshal.Run, error) {
	// A run that will close on its own first checks that the delivery can
	// land: a retry while the checkout is still in the way does not verify
	// the same result again.
	if run, err := current(); err == nil && run.State == marshal.Verifying && run.Settings.AcceptanceMode == marshal.AcceptMarshal && run.ValidCloseAuthorization() {
		if project, err := s.Store.Project(ctx); err == nil && project.DefaultBranch != "" {
			if err := s.deliveryPrecheck(ctx, project.DefaultBranch); err != nil {
				var stop *DeliveryStopError
				if errors.As(err, &stop) {
					return run, err
				}
			}
		}
		// The close verifies the integrated head itself right before its
		// gate. A run made on this runtime has nothing a separate pass would
		// add, so it verifies once, in the close.
		if run.Compatibility != nil && run.Compatibility.Disposition == "current" {
			return s.verifyAndClose(ctx, runID, run, current)
		}
	}
	if _, err := s.VerifyMerged(ctx, runID, unknownMarshalCharge()); err != nil {
		run, _ := current()
		return run, err
	}
	run, err := current()
	if err != nil || run.State != marshal.Verifying {
		return run, err
	}
	if run.Settings.AcceptanceMode == marshal.AcceptMarshal && run.ValidCloseAuthorization() {
		if err := s.Close(ctx, runID); err != nil {
			return run, err
		}
		return current()
	}
	return run, nil
}

// verifyAndClose closes a self-closing run with the close's own
// verification as its only one. The verification itself is unchanged: the
// close runs every approved check and, where required, the fresh independent
// verifier on the exact head it then delivers. Around it this does what a
// separate VerifyMerged did: usage is charged and a budget that needs the
// person stops before any verification; a result that does not pass, or a
// verification that cannot run, leaves the same resumable pause.
func (s *MarshalService) verifyAndClose(ctx context.Context, runID string, run marshal.Run, current func() (marshal.Run, error)) (marshal.Run, error) {
	// The charge is unmeasured usage, so whether it stops the run does not
	// depend on the verification's outcome.
	budget, err := s.Charge(ctx, runID, "", "verification", unknownMarshalCharge())
	if err != nil {
		err = s.pauseVerificationFailure(ctx, runID, err)
		after, _ := current()
		return after, err
	}
	if len(budget.PlanExceeded) > 0 || budgetNeedsOperator(run.Budget, budget) {
		return current()
	}
	head, err := gitMarshal(ctx, filepath.Join(s.Worktrees, integrationTaskID(runID, run)), "rev-parse", "HEAD")
	if err != nil {
		err = s.pauseVerificationFailure(ctx, runID, err)
		after, _ := current()
		return after, err
	}
	s.codeEvidenceVerify(ctx, runID, run, head)
	err = s.closeRun(ctx, runID)
	var stage *closeVerificationError
	if errors.As(err, &stage) {
		if stage.result != "" {
			paused, rev, loadErr := s.load(ctx, runID)
			if loadErr != nil {
				return paused, loadErr
			}
			if paused.State == marshal.Verifying {
				pauseMarshal(&paused, verificationFailedPause(stage.session, stage.result))
				if loadErr = s.save(ctx, runID, paused, rev); loadErr != nil {
					return paused, loadErr
				}
			}
			return current()
		}
		err = s.pauseVerificationFailure(ctx, runID, stage.err)
	}
	after, currentErr := current()
	if err != nil {
		return after, err
	}
	return after, currentErr
}

func marshalDepsMerged(run marshal.Run, t marshal.Task) bool {
	for _, dep := range t.DependsOn {
		j := taskIndex(run, dep)
		if j < 0 || run.Tasks[j].State != marshal.Merged {
			return false
		}
	}
	return true
}

// projectSettingsSnapshot reads the saved project settings snapshot for
// admission-time provider eligibility and failover policy, falling back to
// the run's snapshot when no project settings record has been written yet.
func (s *MarshalService) projectSettingsSnapshot(ctx context.Context, fallback marshal.Settings) marshal.Settings {
	if s == nil || s.Store == nil || s.ProjectID == "" {
		return fallback
	}
	rec, err := s.Store.GetMarshalSettings(ctx, s.ProjectID)
	if err != nil || rec.Revision == 0 {
		return fallback
	}
	return rec.Value
}

// otherWorker picks a configured worker other than current, in a stable order.
func (s *MarshalService) otherWorker(current string, mode marshal.WorkerMode) string {
	return s.otherWorkerFor(context.Background(), marshal.DefaultSettings(), current, mode)
}

// otherWorkerFor picks an eligible configured worker other than current using
// the project settings snapshot for provider enablement and failover order.
func (s *MarshalService) otherWorkerFor(ctx context.Context, fallback marshal.Settings, current string, mode marshal.WorkerMode) string {
	drivers := s.Drivers
	if mode == marshal.Governed && s.GovernedDrivers != nil {
		drivers = s.GovernedDrivers
	}
	policy := s.projectSettingsSnapshot(ctx, fallback)
	names := make([]string, 0, len(drivers))
	for name, d := range drivers {
		if marshalHarnessName(name) != marshalHarnessName(current) && d != nil && d.Mode() == mode && policy.ProviderEnabled(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	names = orderReassignCandidates(policy, names)
	if len(names) == 0 {
		return ""
	}
	// Prefer a worker whose harness is currently verified: an unverified one
	// (no profile, or no explicit model) fails its probe at dispatch and
	// leaves the reassigned task stalled.
	if s.Store != nil {
		for _, name := range names {
			if s.workerGovernance(ctx, name).Governed() {
				return name
			}
		}
	}
	return names[0]
}

// marshalMaxRounds bounds Execute's loop by the work a run can legitimately
// need: every task dispatched, reviewed and merged across all its allowed
// returns and one reassignment, plus verification. Infrastructure attempts
// extend the allowance only as failures are recorded, including unlimited retries.
func marshalMaxRounds(run marshal.Run) int {
	limit := run.Settings.ReworkLimit
	if limit < 1 {
		limit = 1
	}
	return len(run.Tasks)*(limit+2)*3 + 10 + infraRetryRounds(run)
}

// unknownMarshalCharge is usage the runtime could not measure.
func unknownMarshalCharge() marshal.Charge {
	return marshal.Charge{Tokens: marshal.Amount{Known: false}, Money: marshal.Amount{Known: false}}
}

// stalledRunDetail is the raw cause of a stall: how many rounds ran and the
// state of every task that had not finished, for the log and the paused
// card's raw error.
func stalledRunDetail(run marshal.Run, rounds int) string {
	var open []string
	for _, t := range run.Tasks {
		if t.State != marshal.Merged && t.State != marshal.Cancelled {
			open = append(open, t.PlanTaskID+" "+string(t.State))
		}
	}
	return fmt.Sprintf("%d rounds without progress; unfinished tasks: %s", rounds, strings.Join(open, ", "))
}
