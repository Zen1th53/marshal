package app

import "github.com/Zen1th53/marshal/internal/marshal"

// OperatorCanMoveQueuedTask reports whether the operator may move a task that
// has not started yet to another model. It reads the edition's
// governedProviderSwitchAllowed (true in the shipped build, false with
// -tags governed) through one untagged accessor so the TUI never duplicates
// the constant.
func OperatorCanMoveQueuedTask() bool { return governedProviderSwitchAllowed }

// ExecutionCapacity reports the baseline dispatch cap for an observed run.
// Candidate-specific admission checks can reduce available capacity further.
func ExecutionCapacity(gate marshal.CapabilityGate, run marshal.Run) (marshal.DispatchPolicy, int, []string) {
	policy := marshal.TierPolicy(gate, run.Settings)
	cap := policy.Concurrency
	reasons := []string{}
	if gate == nil || !gate.Capability(marshal.CapabilityMarshal) {
		reasons = append(reasons, "ULTRA execution capability is unavailable")
	}
	if g, ok := gate.(interface{ ExecutionEnabled() bool }); ok && !g.ExecutionEnabled() {
		reasons = append(reasons, "ULTRA execution preference is off")
	}
	if policy.Tier == marshal.Ultra {
		if _, global := WorkerConcurrency(run.Settings); global > 0 {
			cap = global
		}
	}
	if shouldStopAfterLaunch(run, marshal.Task{Mode: marshal.Governed}) {
		cap = 1
		if !run.Process05Parallel {
			reasons = append(reasons, "approved plan requires serial work")
		} else {
			reasons = append(reasons, "this edition executes approved plan tasks serially")
		}
	}
	if cap == 1 && len(reasons) == 0 {
		reasons = append(reasons, "configured worker capacity is one")
	}
	active := 0
	for _, task := range run.Tasks {
		if task.State == marshal.Dispatched {
			active++
		}
	}
	if active > cap {
		reasons = append(reasons, "draining existing work before new dispatch")
	}
	return policy, cap, reasons
}
