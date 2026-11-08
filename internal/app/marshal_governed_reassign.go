package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/plan"
)

// errGovernedHarnessDiffers refuses a Process 05 dispatch whose worker is not
// the harness its approved plan assigned to the task.
var errGovernedHarnessDiffers = errors.New("process 05: governed harness differs from the Marshal worker")

const governedHarnessEscalation = "governed harness differs from the approved plan: reassign the task to its plan worker or amend the plan"

// isGovernedHarnessMismatch also recognizes the refusal after a driver has
// carried it across a process boundary as text.
func isGovernedHarnessMismatch(err error) bool {
	return err != nil && (errors.Is(err, errGovernedHarnessDiffers) || strings.Contains(err.Error(), errGovernedHarnessDiffers.Error()))
}

// planProducerHarness names the harness the approved plan assigns to produce
// a task, or "" when the plan assigns none.
func planProducerHarness(p plan.ExecutionPlan, taskID string) string {
	for _, a := range p.Assignments.Assignments {
		if !a.Independent && slices.Contains(a.Tasks, taskID) {
			return a.Harness
		}
	}
	return ""
}

// governedProviderSwitch keeps a Process 05 governed task on the provider its
// approved plan assigned. Every Process 05 check binds the task to that plan's
// producer; only the operator may switch it to another provider, and only in
// an edition that rebinds the switched task (governedProviderSwitchAllowed).
// It returns the plan's worker when the reassignment is such a switch.
func (s *MarshalService) governedProviderSwitch(ctx context.Context, run marshal.Run, t marshal.Task, worker string, operator bool) (string, error) {
	if !run.Process05Bound || t.Mode != marshal.Governed {
		return "", nil
	}
	p, err := s.Store.GetPlan(ctx, run.PlanID, run.PlanVersion)
	if err != nil {
		return "", err
	}
	planned := planProducerHarness(p, t.PlanTaskID)
	if planned != "" && marshalHarnessName(planned) == marshalHarnessName(worker) {
		return "", nil
	}
	// In Extreme the Marshal may choose another worker itself, as the
	// operator may: the task is rebound before its next lane opens.
	if planned != "" && (operator || flexibleWorkerModels) && governedProviderSwitchAllowed {
		return planned, nil
	}
	if planned == "" {
		planned = "none"
	}
	return "", fmt.Errorf("task %s is governed by its approved plan, which assigns worker %s; switching it to %s needs a plan amendment (/marshal amend)", t.PlanTaskID, planned, worker)
}

// escalateGovernedHarnessMismatch hands a refused dispatch to the person
// without counting it as a worker return: no worker ran.
func (s *MarshalService) escalateGovernedHarnessMismatch(ctx context.Context, runID, taskID string) error {
	run, rev, err := s.load(ctx, runID)
	if err != nil {
		return err
	}
	i := taskIndex(run, taskID)
	if i < 0 {
		return errors.New("task not found")
	}
	run.Operation = nil
	run.Tasks[i].State = marshal.Escalated
	run.Tasks[i].EscalationReason = governedHarnessEscalation
	pauseMarshal(&run, governedHarnessEscalation, "reassign the task to its plan worker or amend the plan", "")
	if err = s.save(ctx, runID, run, rev); err != nil {
		return err
	}
	return s.record(ctx, runID, taskID, events.EventTypeMarshalEscalated, map[string]any{"reason": governedHarnessEscalation, "counted": false})
}
