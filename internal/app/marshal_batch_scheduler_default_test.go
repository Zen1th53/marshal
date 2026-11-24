//go:build governed

package app

import (
	"slices"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// The governed edition has no rolling dispatcher: governed plan tasks keep
// batch dispatch, one at a time and in plan order, even under ULTRA, and a
// parallel intent never widens it.
func TestBatchSchedulerRetainsGovernedOrdering(t *testing.T) {
	gate := governedUltraGate{enabled: true}
	settings := marshal.DefaultSettings()
	settings.UltraConcurrency = 4
	if marshal.TierPolicy(gate, settings).Tier != marshal.Ultra {
		t.Fatal("fixture gate did not enable ULTRA")
	}
	task := func(id string) marshal.Task {
		return marshal.Task{PlanTaskID: id, Worker: "codex", Mode: marshal.Governed, State: marshal.Queued, Files: []string{id + ".txt"}}
	}
	run := marshal.Run{Process05Bound: true, Process05Parallel: true, Settings: settings, Tasks: []marshal.Task{task("a"), task("b"), task("c")}}

	admitted := func() []string {
		var ids []string
		var launched []MarshalDispatch
		for i, tk := range run.Tasks {
			if tk.State != marshal.Queued {
				continue
			}
			if ok, _ := canLaunchCandidate(gate, run, tk, launched); !ok {
				continue
			}
			if adm := EvaluateDispatchAdmission(gate, run, tk, i, launched...); !adm.Allowed {
				continue
			}
			ids = append(ids, tk.PlanTaskID)
			launched = append(launched, MarshalDispatch{TaskID: tk.PlanTaskID})
			if shouldStopAfterLaunchGate(gate, run, tk) {
				break
			}
		}
		return ids
	}

	for _, want := range []string{"a", "b", "c"} {
		if got := admitted(); !slices.Equal(got, []string{want}) {
			t.Fatalf("admitted = %v, want only %s", got, want)
		}
		i := taskIndex(run, want)
		run.Tasks[i].State = marshal.Dispatched
		if got := admitted(); len(got) != 0 {
			t.Fatalf("admitted %v while %s holds the batch slot", got, want)
		}
		// Hand-in and review do not open the next task: only merge does.
		run.Tasks[i].State = marshal.Accepted
		if got := admitted(); len(got) != 0 {
			t.Fatalf("admitted %v before %s merged", got, want)
		}
		adm := EvaluateDispatchAdmission(gate, run, run.Tasks[min(i+1, len(run.Tasks)-1)], -1)
		if i+1 < len(run.Tasks) && (adm.Allowed || adm.Code != AdmissionSerialPlan) {
			t.Fatalf("next task admission = %+v, want serial plan blocker", adm)
		}
		run.Tasks[i].State = marshal.Merged
	}
}
