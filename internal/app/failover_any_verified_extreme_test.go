//go:build !governed

package app

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

// The next provider's model comes from its current verified harness profile;
// an expired, unprobed or version-changed profile is refused.
func TestFailoverAnyVerifiedProfileModel(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	s.InstalledVersion = func(context.Context, string) string { return "1.5.0" }
	r := &Runtime{}
	now := time.Now().UTC()
	profile := model.HarnessProfile{Harness: "antigravity", InstalledVersion: "1.5.0", SupportedModels: []string{"gemini-3.8-flash-high"}, DefaultModel: "gemini-3.8-flash-high", ProbeEvidenceID: "EVENT-probe", ProbedAt: now.Add(-time.Minute), ExpiresAt: now.Add(23 * time.Hour), FeatureSupport: map[string]model.FeatureStatus{"headless": model.StatusNative}}
	if err := s.Store.SaveHarnessProfile(t.Context(), profile); err != nil {
		t.Fatal(err)
	}
	if got, err := r.verifiedFallbackProfileModel(t.Context(), s, "agy"); err != nil || got != "gemini-3.8-flash-high" {
		t.Fatalf("verified profile model = %q, %v", got, err)
	}
	s.InstalledVersion = func(context.Context, string) string { return "1.6.0" }
	if _, err := r.verifiedFallbackProfileModel(t.Context(), s, "agy"); err == nil {
		t.Fatal("profile of another installed version accepted")
	}
	s.InstalledVersion = func(context.Context, string) string { return "1.5.0" }
	profile.ExpiresAt = now.Add(-time.Minute)
	profile.ProbedAt = now.Add(-25 * time.Hour)
	if err := s.Store.SaveHarnessProfile(t.Context(), profile); err != nil {
		t.Fatal(err)
	}
	if _, err := r.verifiedFallbackProfileModel(t.Context(), s, "agy"); err == nil {
		t.Fatal("expired profile accepted")
	}
	if _, err := r.verifiedFallbackProfileModel(t.Context(), s, "claude"); err == nil {
		t.Fatal("missing profile accepted")
	}
}

// An automatic provider failover is not a worker rework: the new worker's
// first return goes back to it instead of escalating the task as if a second
// worker had failed it too. (An operator reassignment keeps its own rule.)
func TestFailoverAnyVerifiedProviderFailoverIsNotRework(t *testing.T) {
	run := marshal.Run{Settings: marshal.Settings{ReworkLimit: 4}}
	task := marshal.Task{PlanTaskID: "T", Worker: "agy", ReturnsByAgent: map[string]int{"agy": 2}}
	task.Worker = "opencode"
	retireFailoverReturns(&task, "opencode")
	if len(task.ReturnsByAgent) != 1 || task.ReturnsByAgent["opencode"] != 0 || task.LaneReworkBase != 2 || taskReturns(task) != 2 {
		t.Fatalf("returns not retired: %+v base=%d", task.ReturnsByAgent, task.LaneReworkBase)
	}
	if verdict := applyMarshalReturn(&run, &task); verdict != marshal.VerdictReturn {
		t.Fatalf("first return after failover = %s, want return", verdict)
	}
	if taskReturns(task) != 3 {
		t.Fatalf("lane numbering lost a return: %d", taskReturns(task))
	}
}
