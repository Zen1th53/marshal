//go:build !governed

package execution

import "testing"

// Extreme never stops on a missing plan-sealed fallback: with the runtime's
// verified profile model for exactly the next provider, failover continues;
// the run's goal, plan and task scope still bind it.
func TestFailoverAnyVerifiedUsesProfileWithoutSealedFallback(t *testing.T) {
	e, p, r, task := approvedFallbackFixture(t)
	p.ApprovedScope.ProducerAssignments = nil
	p.ApprovalScopeDigest = p.ApprovedScope.Digest()
	reader := NewMemoryPlanReader()
	reader.AddPlan(p)
	e.planReader = reader
	if _, err := e.approvedFallbackSelector(t.Context(), r, task, "codex", "agy"); err == nil {
		t.Fatal("unsealed fallback accepted without a verified profile")
	}
	ctx := WithVerifiedFallbackModel(t.Context(), "agy", "gemini-3.8-flash-high")
	if model, err := e.approvedFallbackSelector(ctx, r, task, "codex", "agy"); err != nil || model != "gemini-3.8-flash-high" {
		t.Fatalf("verified profile not used: %q %v", model, err)
	}
	// Replay after the rebind keeps the same model; a forged one is refused.
	task.AssignedHarness, task.AssignedModel = "agy", "gemini-3.8-flash-high"
	if model, err := e.approvedFallbackSelector(ctx, r, task, "codex", "agy"); err != nil || model != "gemini-3.8-flash-high" {
		t.Fatalf("replay refused: %q %v", model, err)
	}
	task.AssignedModel = "forged"
	if _, err := e.approvedFallbackSelector(ctx, r, task, "codex", "agy"); err == nil {
		t.Fatal("replay with a changed model accepted")
	}
	task.AssignedHarness, task.AssignedModel = "codex", "primary"
	// The profile must be for exactly the next provider.
	if _, err := e.approvedFallbackSelector(WithVerifiedFallbackModel(t.Context(), "claude", "claude-opus-5-5"), r, task, "codex", "agy"); err == nil {
		t.Fatal("another provider's profile accepted")
	}
	// Plan and task scope still bind it.
	changed := task
	changed.TargetFiles = append([]string{"foreign.go"}, task.TargetFiles...)
	if _, err := e.approvedFallbackSelector(ctx, r, changed, "codex", "agy"); err == nil {
		t.Fatal("changed task scope accepted")
	}
	stale := r
	stale.PlanVersion++
	if _, err := e.approvedFallbackSelector(ctx, stale, task, "codex", "agy"); err == nil {
		t.Fatal("stale plan version accepted")
	}
}

// A sealed fallback still wins over the profile model.
func TestFailoverAnyVerifiedKeepsSealedFallback(t *testing.T) {
	e, _, r, task := approvedFallbackFixture(t)
	ctx := WithVerifiedFallbackModel(t.Context(), "agy", "gemini-3.8-flash-high")
	if model, err := e.approvedFallbackSelector(ctx, r, task, "codex", "agy"); err != nil || model != "reviewed-fallback" {
		t.Fatalf("sealed fallback replaced: %q %v", model, err)
	}
}
