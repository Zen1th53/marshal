package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// MarshalObserver receives the run after every step, so a surface can show
// progress without polling the store.
type MarshalObserver func(marshal.Run)

// MarshalBrief produces the instruction a worker receives for a task.
type MarshalBrief func(marshal.Task) string

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
	current := func() (marshal.Run, error) {
		run, _, err := s.load(ctx, runID)
		if err == nil && observe != nil {
			observe(run)
		}
		return run, err
	}
	for round := 0; ; round++ {
		run, err := current()
		if err != nil {
			return run, err
		}
		// Every round either changes a task's state or stops, and a task can
		// only be returned a bounded number of times, so a healthy run ends
		// well within this bound. Reaching it means something loops; the run
		// is handed to the user rather than left to spend its budget.
		if round > marshalMaxRounds(run) {
			if err := s.Escalate(ctx, runID, "", "the run stopped making progress"); err != nil {
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
		launched, err := s.dispatchReady(ctx, runID, run, brief)
		if err != nil {
			// Workers already launched in this round are still running;
			// collect them so none is left without a hand-in.
			err = errors.Join(err, s.collectAll(ctx, runID, launched, observe, current))
			after, _ := current()
			return after, err
		}
		if len(launched) == 0 {
			// An escalation or reassignment during dispatch changes the run;
			// the next round reports it. Otherwise nothing can move.
			if after, _, err := s.load(ctx, runID); err == nil && after.State != run.State {
				continue
			}
			return run, fmt.Errorf("marshal run %s cannot progress: no task is ready, handed in or accepted", runID)
		}
		if err := s.collectAll(ctx, runID, launched, observe, current); err != nil {
			after, _ := current()
			return after, err
		}
	}
}

// collectAll collects every launched worker, even after one collection
// fails, so no worker is left running without its hand-in recorded. Every
// failure is reported.
func (s *MarshalService) collectAll(ctx context.Context, runID string, launched []MarshalDispatch, observe MarshalObserver, current func() (marshal.Run, error)) error {
	var errs []error
	for _, d := range launched {
		if _, err := s.CollectHandIn(ctx, runID, d); err != nil {
			errs = append(errs, fmt.Errorf("collect %s: %w", d.TaskID, err))
		}
		if observe != nil {
			_, _ = current()
		}
	}
	return errors.Join(errs...)
}

// mergeAccepted merges accepted tasks in plan order, stopping at the first
// task that is not yet accepted: merge order is plan order.
func (s *MarshalService) mergeAccepted(ctx context.Context, runID string, run marshal.Run) (bool, error) {
	progressed := false
	for _, t := range run.Tasks {
		if t.State == marshal.Merged {
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
	policy := marshal.TierPolicy(s.Gate, run.Settings)
	var launched []MarshalDispatch
	for _, t := range run.Tasks {
		if len(launched) >= policy.Concurrency {
			break
		}
		if t.State != marshal.Queued && t.State != marshal.Returned && t.State != marshal.Reassigned {
			continue
		}
		if !marshalDepsMerged(run, t) {
			continue
		}
		if t.State == marshal.Reassigned && len(t.ReturnsByAgent) <= 1 {
			next := s.otherWorker(t.Worker)
			if next == "" {
				return launched, s.Escalate(ctx, runID, t.PlanTaskID, "no other worker is available for reassignment")
			}
			if err := s.Reassign(ctx, runID, t.PlanTaskID, next); err != nil {
				return launched, err
			}
			t.Worker = next
		}
		if s.InstalledVersion != nil {
			if g := s.workerGovernance(ctx, t.Worker); !g.Governed() {
				return launched, s.Escalate(ctx, runID, t.PlanTaskID, fmt.Sprintf("worker %s is not governed (%s): %s", t.Worker, g.State, strings.Join(g.Reasons, " ")))
			}
		}
		d, err := s.Dispatch(ctx, runID, t.PlanTaskID, brief(t))
		if err != nil {
			return launched, err
		}
		launched = append(launched, d)
		if run.Process05Bound && t.Mode == marshal.Governed {
			break
		}
	}
	return launched, nil
}

// finish verifies the integrated result and, when the user granted a
// standing close authorization, closes the run.
func (s *MarshalService) finish(ctx context.Context, runID string, current func() (marshal.Run, error)) (marshal.Run, error) {
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

func marshalDepsMerged(run marshal.Run, t marshal.Task) bool {
	for _, dep := range t.DependsOn {
		j := taskIndex(run, dep)
		if j < 0 || run.Tasks[j].State != marshal.Merged {
			return false
		}
	}
	return true
}

// otherWorker picks a configured worker of another family than current, in a
// stable order. The Marshal model's own family is never picked: the Marshal
// reviews the reassigned work, and must not review its own family's.
func (s *MarshalService) otherWorker(current string) string {
	names := make([]string, 0, len(s.Drivers))
	for name := range s.Drivers {
		if f := marshalFamily(name); f != marshalFamily(current) && f != s.reviewerFamily() {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

// marshalMaxRounds bounds Execute's loop by the work a run can legitimately
// need: every task dispatched, reviewed and merged across all its allowed
// returns and one reassignment, plus verification.
func marshalMaxRounds(run marshal.Run) int {
	limit := run.Settings.ReworkLimit
	if limit < 1 {
		limit = 1
	}
	return len(run.Tasks)*(limit+2)*3 + 10
}

// unknownMarshalCharge is usage the runtime could not measure.
func unknownMarshalCharge() marshal.Charge {
	return marshal.Charge{Tokens: marshal.Amount{Known: false}, Money: marshal.Amount{Known: false}}
}
