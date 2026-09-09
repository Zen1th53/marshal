package optimization

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Recovery after an interrupted optimization cycle.
//
// A crash mid-experiment leaves work in an ambiguous state: results that were
// written, results that were not, and a canary that may still be nominally
// running with nobody watching it. The rule that governs all of it is that an
// interruption never resolves in favour of the optimization. Anything whose
// outcome cannot be established from durable state becomes UNKNOWN, and a
// canary nobody was watching is stopped rather than resumed.

// ResumeAction is what recovery decides to do with one piece of in-flight work.
type ResumeAction string

const (
	// ResumeSafe restarts work that is idempotent and can simply be redone.
	ResumeSafe ResumeAction = "RESUME"
	// ResumeQuarantine keeps a result but excludes it from analysis, because
	// the run was cut short and its outcome cannot be trusted either way.
	ResumeQuarantine ResumeAction = "QUARANTINE"
	// ResumeHaltCanary stops a canary that was running when the process died.
	// It is not resumed: nobody was watching its rollback triggers.
	ResumeHaltCanary ResumeAction = "HALT_CANARY"
	// ResumeBlocked marks work that cannot be reconciled without an operator.
	ResumeBlocked ResumeAction = "BLOCKED"
)

// ResumePlan is one reconciliation decision, with the reason it was made.
type ResumePlan struct {
	// Subject identifies what the decision is about: a result id, a canary id.
	Subject string       `json:"subject"`
	Kind    string       `json:"kind"`
	Action  ResumeAction `json:"action"`
	Reason  string       `json:"reason"`
}

// RecoveryInput is the durable state a restart reads back.
type RecoveryInput struct {
	Cycle Cycle `json:"cycle"`
	// SourceFresh reports whether the Process 07 memory commit the cycle binds
	// to still verifies and still describes current state.
	SourceFresh bool `json:"source_fresh"`
	// Results are every experiment result durably recorded for this cycle.
	Results []ExperimentResult `json:"results"`
	// InFlight names work that was started but has no durable result. These
	// are the ambiguous cases: started, outcome unknown.
	InFlight []string `json:"in_flight"`
	// Canaries are the rollouts recorded against this cycle.
	Canaries []Canary `json:"canaries"`
}

// Recover reconciles an interrupted cycle and returns what may safely continue.
//
// The plan is deterministic and ordered so it can be reviewed before it is
// applied, and every entry carries its reason so a later reader can tell why a
// result was quarantined rather than counted.
func Recover(in RecoveryInput, now time.Time) ([]ResumePlan, error) {
	if err := in.Cycle.Verify(); err != nil {
		// A cycle whose digest no longer matches cannot be reconciled: there
		// is no way to know which parts of it are authentic.
		return nil, fmt.Errorf("recover cycle %s: %w", in.Cycle.ID, err)
	}
	if !in.Cycle.Binding.Valid() {
		return nil, fmt.Errorf("%w: interrupted cycle has no valid Process 07 binding", ErrInvalid)
	}

	var plans []ResumePlan

	// A cycle whose source learning is no longer fresh cannot resume at all.
	// Its candidates were derived from a state that has since moved.
	if !in.SourceFresh {
		plans = append(plans, ResumePlan{
			Subject: in.Cycle.ID, Kind: "cycle", Action: ResumeBlocked,
			Reason: "source memory commit is no longer fresh; the cycle must be rebuilt rather than resumed",
		})
	}

	// Work that started and left no durable result is ambiguous. It is never
	// assumed to have succeeded, and never silently retried into a win.
	for _, subject := range in.InFlight {
		plans = append(plans, ResumePlan{
			Subject: subject, Kind: "experiment", Action: ResumeQuarantine,
			Reason: "interrupted before a durable result was written; outcome is UNKNOWN",
		})
	}

	// Results already durable are kept as they are. An interrupted run that
	// happened to record a PASS is still quarantined if it was marked
	// incomplete, because reaching PASS is not the same as finishing.
	for _, r := range in.Results {
		switch {
		case r.Quarantined:
			plans = append(plans, ResumePlan{
				Subject: r.ID, Kind: "result", Action: ResumeQuarantine,
				Reason: "already quarantined: " + r.QuarantineReason,
			})
		case r.Outcome == StatusUnknown || r.Outcome == StatusNotRun:
			plans = append(plans, ResumePlan{
				Subject: r.ID, Kind: "result", Action: ResumeSafe,
				Reason: "no outcome was established; the task may be re-run",
			})
		}
	}

	// A canary that was live when the process died had nobody evaluating its
	// rollback triggers. Time passed with the candidate controlling traffic
	// and no guard running, so it is halted rather than resumed.
	for _, c := range in.Canaries {
		switch c.State {
		case CanaryRunning:
			plans = append(plans, ResumePlan{
				Subject: c.ID, Kind: "canary", Action: ResumeHaltCanary,
				Reason: "canary was live across a restart with no guard evaluating its triggers",
			})
		case CanaryPending:
			plans = append(plans, ResumePlan{
				Subject: c.ID, Kind: "canary", Action: ResumeSafe,
				Reason: "canary never started; it may be started cleanly",
			})
		}
		// Completed and rolled-back canaries are terminal and need nothing.
	}

	sort.SliceStable(plans, func(i, j int) bool {
		if plans[i].Kind != plans[j].Kind {
			return plans[i].Kind < plans[j].Kind
		}
		return plans[i].Subject < plans[j].Subject
	})
	return plans, nil
}

// ApplyRecovery turns a plan into the concrete state changes it describes.
//
// Quarantined results keep their attempts rather than being deleted, because
// the record of an interrupted run is what tells a later reader why a task has
// no clean outcome.
func ApplyRecovery(plans []ResumePlan, results []ExperimentResult, canaries []Canary, now time.Time) ([]ExperimentResult, []Canary) {
	quarantine := map[string]bool{}
	halt := map[string]bool{}
	for _, p := range plans {
		switch p.Action {
		case ResumeQuarantine:
			quarantine[p.Subject] = true
		case ResumeHaltCanary:
			halt[p.Subject] = true
		}
	}

	outResults := make([]ExperimentResult, 0, len(results))
	for _, r := range results {
		if quarantine[r.ID] && !r.Quarantined {
			r = Quarantine(r, CauseInfraFailure)
		}
		outResults = append(outResults, r)
	}

	outCanaries := make([]Canary, 0, len(canaries))
	for _, c := range canaries {
		if halt[c.ID] {
			c.State = CanaryHalted
			c.RollbackReason = "halted by restart recovery: no guard was evaluating triggers during the interruption"
		}
		outCanaries = append(outCanaries, c)
	}
	return outResults, outCanaries
}

// InterruptedNeverPasses reports whether any plan would let an interrupted
// experiment resolve as a pass.
//
// This is a guard on the recovery logic itself. It exists so the invariant is
// checkable rather than merely intended: no reconciliation may turn an
// interruption into a successful result.
func InterruptedNeverPasses(plans []ResumePlan) error {
	for _, p := range plans {
		if p.Kind == "experiment" && p.Action == ResumeSafe {
			return fmt.Errorf("%w: interrupted experiment %s would resume as complete", ErrInvalid, p.Subject)
		}
		if p.Kind == "canary" && p.Action == ResumeSafe && strings.Contains(p.Reason, "live") {
			return fmt.Errorf("%w: live canary %s would resume unguarded", ErrInvalid, p.Subject)
		}
	}
	return nil
}
