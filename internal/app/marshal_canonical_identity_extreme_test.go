//go:build !governed

package app

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
)

func TestCanonicalApprovalRetainsRuntimeAndWorkerSelection(t *testing.T) {
	r, draft, profile := canonicalNativeBridgeFixture(t)
	if err := r.Store().SaveHarnessProfile(t.Context(), profile); err != nil {
		t.Fatal(err)
	}
	canonical, err := r.PrepareNativeMarshalDraft(t.Context(), "SESSION-required", draft)
	if err != nil {
		t.Fatal(err)
	}
	canonical.Tasks[0].WorkerSelection = &marshal.WorkerSelection{RequestedWorker: "opencode", SelectedModel: profile.DefaultModel, Reason: "requested provider has no verified harness"}
	s := r.Marshal()
	s.InstalledVersion = func(context.Context, string) string { return "fixture-1" }
	s.GovernedDrivers = map[string]driver.Driver{"codex": driver.Governed{Provider: "codex"}}
	s.ApprovalActor = func(context.Context, string, string) (string, error) { return "owner", nil }
	started, err := s.StartPlanningFromDraft(t.Context(), "identity", "Fix the documented typo.", canonical, marshal.Budget{})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.PlanApprovalSnapshot(t.Context(), "identity")
	if err != nil {
		t.Fatal(err)
	}
	approved, err := s.Approve(t.Context(), "identity", snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if approved.Compatibility == nil || *approved.Compatibility != *started.Compatibility {
		t.Fatalf("approval lost runtime origin: %+v", approved.Compatibility)
	}
	if approved.Tasks[0].WorkerSelection == nil || *approved.Tasks[0].WorkerSelection != *canonical.Tasks[0].WorkerSelection {
		t.Fatalf("approval lost worker selection: %+v", approved.Tasks[0])
	}
	if err := validateMarshalTaskIdentity("identity", approved.Tasks[0]); err != nil {
		t.Fatal(err)
	}
}
