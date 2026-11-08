//go:build !governed

package app

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/projectid"
)

// Exercise the real Runtime composition, not a manually installed guard. A
// genuine accepted memory contract alone is not a canonical execution grant.
func TestRequiredContextActualMarshalDispatchCannotUseLegacyRoute(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing-contract", true: "accepted-contract-without-bound-plan"}[accepted], func(t *testing.T) {
			var r *Runtime
			if accepted {
				r, _, _, _ = requiredContextFixture(t)
			} else {
				r = openNetpolRuntime(t)
			}
			s := r.Marshal()
			fixture, _ := marshalFixture(t, 1)
			fake := fixture.Model.(marshalFakeModel)
			fake.draft.Plan.ProjectID = projectid.ID(r.ProjectIdentity())
			s.Model, s.ApprovalActor, s.GateState = fake, fixture.ApprovalActor, fixture.GateState
			var calls atomic.Int32
			d := driver.Governed{Provider: "worker", Run: func(context.Context, driver.Request) ([]marshal.CommandRecord, error) {
				calls.Add(1)
				return nil, nil
			}}
			s.Drivers = map[string]driver.Driver{"worker": d}
			s.GovernedDrivers = map[string]driver.Driver{"worker": d}
			if _, err := s.StartPlanningFromDraft(t.Context(), "legacy-dispatch", "write one file", fake.draft, marshal.Budget{}); err != nil {
				t.Fatal(err)
			}
			// Seed a historical approval directly. Current runtime-owned
			// Approve correctly refuses this draft, but rows approved before
			// that guard must still be refused at the dispatch boundary.
			run, revision, err := s.readRun(t.Context(), "legacy-dispatch")
			if err != nil {
				t.Fatal(err)
			}
			p, err := s.Store.GetPlan(t.Context(), run.PlanID, run.PlanVersion)
			if err != nil {
				t.Fatal(err)
			}
			approved, err := p.Approve(s.clock())
			if err != nil {
				t.Fatal(err)
			}
			run.PlanVersion = p.Version + 1
			run.State = marshal.Approved
			run.ApprovalScopeDigest = marshalApprovalDigest(approved.ApprovalScopeDigest, run)
			run.GoverningDigest, err = governingDigest(s.Repository)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Store.SaveMarshalPlanState(t.Context(), s.ProjectID, "legacy-dispatch", approved, p.Version, run, revision, nil); err != nil {
				t.Fatal(err)
			}
			before, beforeRevision, err := s.readRun(t.Context(), "legacy-dispatch")
			if err != nil {
				t.Fatal(err)
			}
			if before.State != marshal.Approved || before.Process05Bound || before.ApprovalScopeDigest == "" {
				t.Fatal("fixture is not a persisted legacy approved unbound governed run")
			}
			if _, err := s.Approve(t.Context(), "legacy-dispatch"); err == nil {
				t.Fatal("historical unbound governed approval was replayed as executable")
			}
			result, err := s.Dispatch(t.Context(), "legacy-dispatch", "a", "write one file")
			if !errors.Is(err, projectcontext.ErrReviewPending) || result.Handle != nil || calls.Load() != 0 {
				t.Fatal("actual Runtime Marshal dispatch bypassed required context", err, calls.Load())
			}
			after, afterRevision, err := s.readRun(t.Context(), "legacy-dispatch")
			if err != nil || afterRevision != beforeRevision || !reflect.DeepEqual(after, before) {
				t.Fatal("refused dispatch acquired a worker lane", err)
			}
		})
	}
}

func TestRequiredMarshalDispatchPreservesOnlyUnchangedApplicableScope(t *testing.T) {
	for _, scenario := range []string{"beta-only", "alpha", "architecture"} {
		t.Run(scenario, func(t *testing.T) {
			r, _, a, _ := requiredWatchFixture(t, true)
			run := marshal.Run{Process05Bound: true, PlanID: a.Binding.PlanID, PlanVersion: a.Binding.PlanVersion}
			task := marshal.Task{PlanTaskID: a.Binding.TaskID, Mode: marshal.Governed}
			if err := r.requiredMarshalDispatchAdmission(t.Context(), run, task); err != nil {
				t.Fatal(err)
			}
			accepted, err := r.store.ReadAcceptedProjectContract(t.Context(), a.Binding.Project)
			if err != nil {
				t.Fatal(err)
			}
			next := accepted.Contract
			next.Required = append([]projectcontext.MemoryRef(nil), next.Required...)
			if scenario == "architecture" {
				next.Purpose = "Changed architecture purpose"
			} else {
				id := "MEM-private-beta"
				if scenario == "alpha" {
					id = "MEM-required-resolver"
				}
				record, err := r.store.GetMemoryV2(t.Context(), r.ProjectID(), id)
				if err != nil {
					t.Fatal(err)
				}
				record, err = r.store.UpdateMemory(t.Context(), record.ProjectID, record.ID, record.Revision, func(m *model.MemoryRecordV2) error { m.Body = "Changed reviewed policy"; return nil })
				if err != nil {
					t.Fatal(err)
				}
				for i := range next.Required {
					if next.Required[i].ID == id {
						next.Required[i].Revision = record.Revision
						next.Required[i].ContentDigest = record.ContentDigest
						next.Required[i].StateFingerprint = projectcontext.MemoryFingerprint(record)
					}
				}
			}
			acceptWatchContract(t, r, next, "dispatch-scope-"+scenario)
			err = r.requiredMarshalDispatchAdmission(t.Context(), run, task)
			if scenario == "beta-only" {
				if err != nil {
					t.Fatal("unrelated beta revision blocked unchanged alpha", err)
				}
			} else if !errors.Is(err, projectcontext.ErrChanged) {
				t.Fatal("changed applicable scope retained stale authority", err)
			}
		})
	}
}
