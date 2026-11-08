//go:build !governed

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/statepath"
)

type waitExpiryModel struct{ calls atomic.Int32 }

func (m *waitExpiryModel) Draft(context.Context, string) (MarshalDraft, error) {
	m.calls.Add(1)
	return MarshalDraft{}, errors.New("unexpected model call")
}
func (m *waitExpiryModel) Review(context.Context, marshal.Task, marshal.HandIn, marshal.Control) (marshal.Review, error) {
	m.calls.Add(1)
	return marshal.Review{}, errors.New("unexpected model call")
}
func (m *waitExpiryModel) Amend(context.Context, marshal.Run, string) (MarshalDraft, error) {
	m.calls.Add(1)
	return MarshalDraft{}, errors.New("unexpected model call")
}

func TestExtremeProcess05RequestedExpiryRefusesResumeWithoutChangingReceipt(t *testing.T) {
	ctx := t.Context()
	databasePath := filepath.Join(t.TempDir(), "state.db")
	fixture, _ := marshalFixture(t, 1, databasePath)
	settings, err := fixture.Store.GetMarshalSettings(ctx, fixture.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	settings.Value.AcceptanceMode = marshal.AcceptMarshalThenUser
	if _, err := fixture.Store.SetMarshalSettings(ctx, fixture.ProjectID, settings.Value, settings.Revision); err != nil {
		t.Fatal(err)
	}
	runtime := requiredProcess05TestRuntime(t, fixture)
	layout := runtime.layout
	databasePath = layout.Database
	goal := planGoal()
	goal.ID, goal.SessionID, goal.ProjectID = "GOAL-expiry", "SESSION-expiry", runtime.ProjectIdentity()
	if err := fixture.Store.SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatal(err)
	}
	worker := requiredProcess05TestHarnessName("test-harness")
	request := requiredProcess05PlanRequest(planCreateRequest())
	request.SessionID, request.ProjectID = goal.SessionID, projectid.ID(runtime.ProjectIdentity())
	p, err := runtime.Plans().Create(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	p.Checks = map[string][]string{"fix": {"grep -q fixed README.md"}}
	if err := fixture.Store.SavePlan(ctx, p, p.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Plans().Approve(ctx, projectid.ID(runtime.ProjectIdentity())); err != nil {
		t.Fatal(err)
	}
	wiring := MarshalWiring{Provider: "codex", Approver: func(context.Context, string, string) (string, error) { return "fixture-operator", nil }}
	s, err := runtime.MarshalWired(wiring)
	if err != nil {
		t.Fatal(err)
	}
	modelCalls := &waitExpiryModel{}
	s.Model = modelCalls
	s.GovernedDrivers[worker] = driver.Governed{Provider: worker, Run: runtime.marshalProcess05Run(s)}
	s.InstalledVersion = func(context.Context, string) string { return "1.0" }
	if err := fixture.Store.SaveHarnessProfile(ctx, model.HarnessProfile{Harness: worker, InstalledVersion: "1.0", ProbeEvidenceID: "expiry-probe", ProbedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	var workerCalls atomic.Int32
	register := func(r *Runtime) {
		requiredRegisterProcess05TestHarness(t, r, worker, func(context.Context, execution.TaskExecution, execution.ConstraintPackage, string) (execution.TaskResult, error) {
			workerCalls.Add(1)
			return execution.TaskResult{}, errors.New("unexpected worker call")
		})
	}
	register(runtime)
	if _, err := s.BindApprovedPlan(ctx, "expiry-run"); err != nil {
		t.Fatal(err)
	}
	waiting, err := s.Execute(ctx, "expiry-run", marshalBrief, nil)
	runs, listErr := runtime.Execution().ListRuns(ctx)
	if err != nil || listErr != nil || len(runs) != 1 {
		t.Fatalf("actual governed approval pause: %+v %v %v", waiting, err, listErr)
	}
	assertMarshalProcess05Pause(t, s, "expiry-run", "fix", waiting, err, runs[0])
	id := runs[0].Tasks["fix"].ApprovalID
	path := filepath.Join(layout.Root, statepath.DirName, "approvals", id+".json")
	receiptBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := runtime.Execution().Engine().ApprovalManager().GetApproval(id)
	if err != nil || receipt.Status != execution.ApprovalRequested || receipt.ExpiresAt == nil {
		t.Fatalf("exact requested receipt: %+v %v", receipt, err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	fresh, err := OpenWithOptions(ctx, layout.Root, Options{Adapters: runtime.adapters})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fresh.Close() })
	register(fresh)
	s, err = fresh.MarshalWired(wiring)
	if err != nil {
		t.Fatal(err)
	}
	s.Model = modelCalls
	before, revision, err := s.load(ctx, "expiry-run")
	if err != nil {
		t.Fatal(err)
	}
	executionBefore, err := fresh.Execution().GetRun(ctx, runs[0].RunID)
	if err != nil {
		t.Fatal(err)
	}
	for _, clock := range []struct {
		name    string
		now     time.Time
		expired bool
	}{{"live", receipt.ExpiresAt.Add(-time.Second), false}, {"exact-boundary", *receipt.ExpiresAt, true}, {"past", receipt.ExpiresAt.Add(time.Second), true}} {
		t.Run(clock.name, func(t *testing.T) {
			s.now = func() time.Time { return clock.now }
			got, err := s.Resume(ctx, "expiry-run")
			if clock.expired && (err == nil || !strings.Contains(err.Error(), "expired")) || !clock.expired && err != nil {
				t.Fatalf("expiry classification: %+v %v", got, err)
			}
			if !reflect.DeepEqual(got, before) {
				t.Fatalf("resume changed the exact paused Marshal run: %+v", got)
			}
		})
	}
	// Expiry cannot mask a foreign plan binding or confer continuation rights.
	foreign := before
	foreign.PlanID = "PLAN-foreign"
	if pending, err := s.process05WaitReceipt(ctx, foreign, before.Tasks[0], runs[0].RunID, id); pending || !errors.Is(err, execution.ErrRunConflict) {
		t.Fatalf("foreign context was accepted: %v %v", pending, err)
	}
	after, afterRevision, err := s.load(ctx, "expiry-run")
	if err != nil || revision != afterRevision || !reflect.DeepEqual(before, after) {
		t.Fatalf("resume rewrote orchestration: %+v revision=%d %v", after, afterRevision, err)
	}
	executionAfter, err := fresh.Execution().GetRun(ctx, runs[0].RunID)
	if err != nil || !reflect.DeepEqual(executionBefore, executionAfter) {
		t.Fatalf("resume rewrote actual execution: %+v %v", executionAfter, err)
	}
	afterBytes, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(receiptBytes, afterBytes) {
		t.Fatal("read-only expiry check changed approval bytes")
	}
	afterReceipt, err := fresh.Execution().Engine().ApprovalManager().GetApproval(id)
	if err != nil || !reflect.DeepEqual(receipt, afterReceipt) || workerCalls.Load() != 0 || modelCalls.calls.Load() != 0 {
		t.Fatalf("receipt renewal or unexpected execution: %+v worker=%d model=%d %v", afterReceipt, workerCalls.Load(), modelCalls.calls.Load(), err)
	}
	// Existing persisted EXPIRED receipts retain their refusal behavior.
	if _, err := fresh.Execution().Engine().ApprovalManager().Decide(id, true, "fixture-operator", "expired", receipt.ExpiresAt.Add(time.Second)); err == nil {
		t.Fatal("canonical manager accepted an expired approval")
	}
	persisted, _ := fresh.Execution().Engine().ApprovalManager().GetApproval(id)
	if pending, err := s.process05WaitReceipt(ctx, before, before.Tasks[0], runs[0].RunID, id); pending || err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("persisted expired receipt was accepted: %v %v", pending, err)
	}
	current, _ := fresh.Execution().Engine().ApprovalManager().GetApproval(id)
	a, _ := json.Marshal(persisted)
	b, _ := json.Marshal(current)
	if !bytes.Equal(a, b) {
		t.Fatal("expired receipt read changed canonical status or revision")
	}
}
