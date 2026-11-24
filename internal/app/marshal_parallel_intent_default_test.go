//go:build governed

package app

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
)

type governedUltraGate struct {
	enabled bool
}

func (governedUltraGate) Capability(name string) bool { return name == marshal.CapabilityMarshal }
func (g governedUltraGate) ExecutionEnabled() bool    { return g.enabled }

func TestGovernedProcess05StillDispatchesSerially(t *testing.T) {
	gate := governedUltraGate{enabled: true}
	settings := marshal.DefaultSettings()
	if marshal.TierPolicy(gate, settings).Tier != marshal.Ultra {
		t.Fatal("fixture gate did not enable ULTRA")
	}

	// Governed edition drafts keep plan.ModeStandard even under an active ULTRA gate.
	if mode := EffectiveDraftPlanMode(gate, settings); mode != plan.ModeStandard {
		t.Fatalf("governed EffectiveDraftPlanMode = %q, want %q", mode, plan.ModeStandard)
	}
	cli := &MarshalCLI{
		ProjectID: "PROJECT-0123456789abcdef0123456789abcdef",
		Gate:      gate,
		Settings:  settings,
	}
	var prop marshalTaskProposal
	if err := json.Unmarshal([]byte(`{"tasks":[`+
		`{"id":"a","title":"task a","worker":"codex","mode":"governed","criteria":["a ok"],"paths":["a.txt"],"checks":[{"command":"true","criteria":["a ok"]}]},`+
		`{"id":"b","title":"task b","worker":"claude","mode":"governed","criteria":["b ok"],"paths":["b.txt"],"checks":[{"command":"true","criteria":["b ok"]}]}]}`), &prop); err != nil {
		t.Fatal(err)
	}
	draft, err := cli.materialize(prop, "", 1, []string{"codex", "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if draft.Plan.Mode != plan.ModeStandard {
		t.Fatalf("governed CLI draft mode = %q, want %q", draft.Plan.Mode, plan.ModeStandard)
	}

	// Even an approved ULTRA plan never enables Process05Parallel in the governed edition.
	ultraPlan := plan.ExecutionPlan{
		ID:                  "PLAN-gov-ultra",
		ProjectID:           projectid.ID("PROJECT-0123456789abcdef0123456789abcdef"),
		Goal:                plan.GoalBinding{GoalID: "GOAL-gov", Revision: 1},
		Version:             1,
		State:               plan.StateReady,
		Mode:                plan.ModeUltra,
		ConstitutionVersion: constitution.Current,
		Tasks: []plan.Task{
			{ID: "a", Title: "task a", Paths: []string{"a.txt"}, Criteria: []string{"a ok"}, Mutating: true, Weight: 1},
			{ID: "b", Title: "task b", Paths: []string{"b.txt"}, Criteria: []string{"b ok"}, Mutating: true, Weight: 1},
		},
		Checks: map[string][]string{"a": {"true"}, "b": {"true"}},
	}
	graph, err := plan.BuildGraph(ultraPlan.Tasks)
	if err != nil {
		t.Fatal(err)
	}
	ultraPlan.Graph = graph
	approved, err := ultraPlan.Approve(time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}

	run := marshal.Run{
		Process05Bound: true,
		Settings:       settings,
		Tasks: []marshal.Task{
			{PlanTaskID: "a", Mode: marshal.Governed, Worker: "codex", Files: []string{"a.txt"}, State: marshal.Queued},
			{PlanTaskID: "b", Mode: marshal.Governed, Worker: "claude", Files: []string{"b.txt"}, State: marshal.Queued},
		},
	}
	bindPrivateProcess05Tier(&run, approved)
	if run.Process05Parallel {
		t.Fatal("governed bindPrivateProcess05Tier enabled Process05Parallel")
	}

	// Even if Process05Parallel were set true, governed Process 05 still dispatches serially with serial_plan.
	run.Process05Parallel = true
	if !shouldStopAfterLaunchGate(gate, run, run.Tasks[0]) {
		t.Fatal("governed Process 05 launch did not stop ready batch")
	}
	admQueued := EvaluateDispatchAdmission(gate, run, run.Tasks[1], 1)
	if admQueued.Allowed || admQueued.Code != AdmissionSerialPlan || !slices.Equal(admQueued.BlockingTaskIDs, []string{"a"}) {
		t.Fatalf("governed Process 05 admitted successor before predecessor merged: %+v", admQueued)
	}
	if can, stop := canLaunchCandidate(gate, run, run.Tasks[1], nil); can || !stop {
		t.Fatalf("canLaunchCandidate = (%v,%v), want (false,true)", can, stop)
	}
	run.Tasks[0].State = marshal.Dispatched
	err = checkDispatchAdmission(gate, run, run.Tasks[1], 1)
	var admErr *DispatchAdmissionError
	if !errors.As(err, &admErr) || admErr.Admission.Code != AdmissionSerialPlan || !slices.Equal(admErr.Admission.BlockingTaskIDs, []string{"a"}) {
		t.Fatalf("checkDispatchAdmission = %v, want *DispatchAdmissionError(serial_plan)", err)
	}
	run.Tasks[0].State = marshal.Merged
	if err := checkDispatchAdmission(gate, run, run.Tasks[1], 1); err != nil {
		t.Fatalf("merged predecessor still blocked successor: %v", err)
	}

	// Standard tier and unmerged dependencies also carry typed admission codes.
	run.Process05Bound = false
	run.Tasks[0].State = marshal.Dispatched
	run.Tasks[1].State = marshal.Queued
	stdGate := governedUltraGate{enabled: false}
	stdAdm := EvaluateDispatchAdmission(stdGate, run, run.Tasks[1], 1)
	if stdAdm.Allowed || stdAdm.Code != AdmissionStandardTier || !slices.Equal(stdAdm.BlockingTaskIDs, []string{"a"}) {
		t.Fatalf("standard tier admission = %+v, want standard_tier", stdAdm)
	}
	run.Tasks[1].DependsOn = []string{"a"}
	depAdm := EvaluateDispatchAdmission(gate, run, run.Tasks[1], 1)
	if depAdm.Allowed || depAdm.Code != AdmissionDependencyUnmerged || !slices.Equal(depAdm.BlockingTaskIDs, []string{"a"}) {
		t.Fatalf("dependency admission = %+v, want dependency_unmerged", depAdm)
	}
}
