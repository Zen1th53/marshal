package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/plan"
)

func setControl(t *testing.T, s *MarshalService, control marshal.Control) {
	t.Helper()
	record, err := s.Store.GetMarshalSettings(t.Context(), s.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	settings := record.Value
	settings.Control = control
	if _, err := s.Store.SetMarshalSettings(t.Context(), s.ProjectID, settings, record.Revision); err != nil {
		t.Fatal(err)
	}
}

func TestStrictControlNeedsInstructionsForEveryTask(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	setControl(t, s, marshal.ControlStrict)
	draft := s.Model.(marshalFakeModel).draft
	if _, err := s.StartPlanningFromDraft(t.Context(), "bare", "goal", draft, marshal.Budget{}); err == nil || !strings.Contains(err.Error(), "strict control needs instructions") {
		t.Fatalf("strict plan without instructions: %v", err)
	}
	draft.Plan.Tasks[0].Instructions = "write the file with one line"
	draft.Tasks[0].Instructions = "write the file with one line"
	if _, err := s.StartPlanningFromDraft(t.Context(), "run", "goal", draft, marshal.Budget{}); err != nil {
		t.Fatalf("strict plan with instructions: %v", err)
	}
}

func TestMarshalTaskInstructionsMustMatchThePlan(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	draft := s.Model.(marshalFakeModel).draft
	draft.Plan.Tasks[0].Instructions = "approved text"
	draft.Tasks[0].Instructions = "something else"
	if _, err := s.StartPlanningFromDraft(t.Context(), "run", "goal", draft, marshal.Budget{}); err == nil || !strings.Contains(err.Error(), "exceeds plan") {
		t.Fatalf("task instructions differing from the plan: %v", err)
	}
}

// Free control keeps the digest a run had before control levels existed;
// strict control is bound into it, so approval covers the level too.
func TestApprovalDigestBindsControlLevel(t *testing.T) {
	budget := marshal.Budget{}
	data, _ := json.Marshal(struct {
		Plan   string
		Budget marshal.Budget
	}{"sha256:plan", budget})
	sum := sha256.Sum256(data)
	before := "sha256:" + hex.EncodeToString(sum[:])
	run := marshal.Run{Budget: budget, Settings: marshal.Settings{Control: marshal.ControlFree}}
	if got := marshalApprovalDigest("sha256:plan", run); got != before {
		t.Fatalf("free digest changed: %s, was %s", got, before)
	}
	run.Settings.Control = marshal.ControlStrict
	if marshalApprovalDigest("sha256:plan", run) == before {
		t.Fatal("strict control is not bound into the approval digest")
	}
}

// The reasons an attempt was returned for reach the next brief, and the
// brief a worker was sent is recorded with its digest.
func TestReturnReasonsReachTheNextBriefAndBriefsAreRecorded(t *testing.T) {
	ctx := context.Background()
	s, _ := marshalFixture(t, 1)
	if _, err := s.StartPlanning(ctx, "run", "write file", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	draft := s.Model.(marshalFakeModel).draft
	s.Model = marshalFakeModel{draft: draft, review: marshal.Review{Verdict: marshal.VerdictReturn, Reviewer: "marshal", Reasons: []string{"the file is empty"}}}
	d, err := s.Dispatch(ctx, "run", "a", "first brief")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CollectHandIn(ctx, "run", d); err != nil {
		t.Fatal(err)
	}
	if v, err := s.Review(ctx, "run", "a", knownCharge()); err != nil || v != marshal.VerdictReturn {
		t.Fatalf("review: %s %v", v, err)
	}
	run, _, err := s.load(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	bc, err := s.briefContext(ctx, "run", run, run.Tasks[0])
	if err != nil || bc.Control != marshal.ControlFree || len(bc.Returned) != 1 || bc.Returned[0] != "the file is empty" {
		t.Fatalf("brief context: %+v %v", bc, err)
	}
	decisions, err := s.Store.MarshalDecisions(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256([]byte("first brief"))
	for _, event := range decisions {
		if event.Data["brief"] == "first brief" && event.Data["brief_sha256"] == hex.EncodeToString(want[:]) {
			return
		}
	}
	t.Fatal("the sent brief was not recorded with its digest")
}

// Per-task control drives the brief: a strict task's brief holds workers to
// the instructions, a free task's in the same run does not. A strict run
// setting overrides a per-task free, keeping every task strict.
func TestBriefControlPerTask(t *testing.T) {
	ctx := context.Background()
	s, _ := marshalFixture(t, 2)
	draft := s.Model.(marshalFakeModel).draft
	draft.Plan.Tasks[0].Control, draft.Tasks[0].Control = "strict", marshal.ControlStrict
	draft.Plan.Tasks[0].Instructions, draft.Tasks[0].Instructions = "write the file", "write the file"
	draft.Plan.Tasks[1].Control, draft.Tasks[1].Control = "free", marshal.ControlFree
	// The graph digest binds the task fields, so rebuild it after setting control.
	graph, err := plan.BuildGraph(draft.Plan.Tasks)
	if err != nil {
		t.Fatal(err)
	}
	draft.Plan.Graph = graph
	if _, err := s.StartPlanningFromDraft(ctx, "run", "goal", draft, marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	run, _, err := s.load(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	if got := run.TaskControl(run.Tasks[0]); got != marshal.ControlStrict {
		t.Fatalf("strict task control: %q", got)
	}
	if got := run.TaskControl(run.Tasks[1]); got != marshal.ControlFree {
		t.Fatalf("free task control: %q", got)
	}
	bc0, err := s.briefContext(ctx, "run", run, run.Tasks[0])
	if err != nil || bc0.Control != marshal.ControlStrict {
		t.Fatalf("strict brief control: %q %v", bc0.Control, err)
	}
	bc1, err := s.briefContext(ctx, "run", run, run.Tasks[1])
	if err != nil || bc1.Control != marshal.ControlFree {
		t.Fatalf("free brief control: %q %v", bc1.Control, err)
	}
	// A strict run keeps the free task strict.
	run.Settings.Control = marshal.ControlStrict
	if got := run.TaskControl(run.Tasks[1]); got != marshal.ControlStrict {
		t.Fatalf("strict run should keep free task strict: %q", got)
	}
}
