package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProviderStatusDetailsDiscoveryAndObservations(t *testing.T) {
	ctx := context.Background()
	r := &Runtime{}

	details, err := r.ProviderStatusDetails(ctx)
	if err != nil {
		t.Fatalf("ProviderStatusDetails failed: %v", err)
	}
	if len(details) == 0 {
		t.Fatal("expected discovered providers, got 0")
	}

	foundMap := make(map[string]ProviderStatusDetail)
	for _, d := range details {
		foundMap[d.Canonical] = d
		if d.QuotaReason == "" || !strings.Contains(d.QuotaReason, "quota") {
			t.Errorf("provider %s quota reason empty or misleading: %q", d.Name, d.QuotaReason)
		}
		if d.ResetReason == "" || !strings.Contains(d.ResetReason, "reset") {
			t.Errorf("provider %s reset reason empty or misleading: %q", d.Name, d.ResetReason)
		}
		if d.AllowanceReason == "" || !strings.Contains(d.AllowanceReason, "allowance") {
			t.Errorf("provider %s allowance reason empty or misleading: %q", d.Name, d.AllowanceReason)
		}
	}

	for _, expected := range []string{"codex", "claude", "opencode", "agy"} {
		d, ok := foundMap[expected]
		if !ok {
			t.Errorf("expected canonical provider %s not found in details", expected)
			continue
		}
		if d.Name == "" {
			t.Errorf("expected display name for %s, got empty", expected)
		}
	}
}

func TestProviderAuthObservationFreshness(t *testing.T) {
	now := time.Now().UTC()
	fresh := ProviderAuthObservation{
		Provider:   "codex",
		Status:     AuthStatusSignedIn,
		Reason:     "codex reported signed in",
		Source:     "test",
		ObservedAt: now.Add(-10 * time.Second),
	}
	if !fresh.IsFresh(30*time.Second, now) {
		t.Error("expected 10s old observation to be fresh within 30s")
	}

	stale := ProviderAuthObservation{
		Provider:   "codex",
		Status:     AuthStatusSignedIn,
		Reason:     "codex reported signed in",
		Source:     "test",
		ObservedAt: now.Add(-90 * time.Second),
	}
	if stale.IsFresh(30*time.Second, now) {
		t.Error("expected 90s old observation to be stale within 30s")
	}
}

func TestProviderUsageMeasuredZeroDistinctFromUnknown(t *testing.T) {
	ctx := context.Background()
	r := &Runtime{}

	// Test measured zero
	zeroTokens := int64(0)
	zeroCost := float64(0.0)
	r.RecordProviderUsage("codex", ProviderUsageObservation{
		Reported:    true,
		TotalTokens: &zeroTokens,
		CostUSD:     &zeroCost,
		ModelCalls:  1,
		Source:      "test-run",
		ObservedAt:  time.Now().UTC(),
	})

	detail, err := r.ProviderStatusDetail(ctx, "codex")
	if err != nil {
		t.Fatalf("ProviderStatusDetail failed: %v", err)
	}
	if !detail.Usage.Reported {
		t.Fatal("expected usage to be reported")
	}
	if detail.Usage.TotalTokens == nil || *detail.Usage.TotalTokens != 0 {
		t.Fatalf("expected measured zero tokens, got %v", detail.Usage.TotalTokens)
	}
	if detail.Usage.CostUSD == nil || *detail.Usage.CostUSD != 0.0 {
		t.Fatalf("expected measured zero cost, got %v", detail.Usage.CostUSD)
	}

	// Test unknown tokens
	r.RecordProviderUsage("claude", ProviderUsageObservation{
		Reported:    true,
		TotalTokens: nil,
		CostUSD:     nil,
		ModelCalls:  2,
		Source:      "test-run",
		ObservedAt:  time.Now().UTC(),
	})

	claudeDetail, err := r.ProviderStatusDetail(ctx, "claude")
	if err != nil {
		t.Fatalf("ProviderStatusDetail claude failed: %v", err)
	}
	if claudeDetail.Usage.TotalTokens != nil {
		t.Fatalf("expected nil total tokens for unreported usage, got %v", *claudeDetail.Usage.TotalTokens)
	}
	if claudeDetail.Usage.CostUSD != nil {
		t.Fatalf("expected nil cost for unreported usage, got %v", *claudeDetail.Usage.CostUSD)
	}
}

func TestProviderCooldownObservationLifecycle(t *testing.T) {
	ctx := context.Background()
	r := &Runtime{}
	now := time.Now().UTC()

	// Active cooldown
	r.RecordProviderCooldown("opencode", ProviderCooldownObservation{
		Active:     true,
		RetryAfter: 15 * time.Second,
		ResetTime:  now.Add(15 * time.Second),
		Source:     "HTTP 429",
		Reason:     "rate limit exceeded",
		ObservedAt: now,
	})

	detail, err := r.ProviderStatusDetail(ctx, "opencode")
	if err != nil {
		t.Fatalf("ProviderStatusDetail failed: %v", err)
	}
	if !detail.Cooldown.Active {
		t.Fatal("expected cooldown to be active")
	}
	if detail.Cooldown.RetryAfter != 15*time.Second {
		t.Fatalf("expected 15s retry after, got %v", detail.Cooldown.RetryAfter)
	}

	// Expired cooldown
	r.RecordProviderCooldown("opencode", ProviderCooldownObservation{
		Active:     true,
		RetryAfter: 5 * time.Second,
		ResetTime:  now.Add(-10 * time.Second),
		Source:     "HTTP 429",
		Reason:     "rate limit exceeded",
		ObservedAt: now.Add(-15 * time.Second),
	})

	detailExpired, err := r.ProviderStatusDetail(ctx, "opencode")
	if err != nil {
		t.Fatalf("ProviderStatusDetail failed: %v", err)
	}
	if detailExpired.Cooldown.Active {
		t.Fatal("expected expired cooldown to be marked inactive")
	}
}

func TestProviderStatusProbeHook(t *testing.T) {
	// The hook only runs for a provider whose executable was found, so the
	// test supplies its own instead of relying on the host having codex.
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")

	restore := SetAuthProbeHookForTest(func(ctx context.Context, provider, binary string) (ProviderAuthObservation, bool) {
		if provider == "codex" {
			return ProviderAuthObservation{
				Provider:   "codex",
				Status:     AuthStatusSignedIn,
				Reason:     "test hook reported signed in",
				Source:     "test-hook",
				ObservedAt: time.Now().UTC(),
			}, true
		}
		return ProviderAuthObservation{}, false
	})
	defer restore()

	r := &Runtime{}
	detail, err := r.ProviderStatusDetail(context.Background(), "codex")
	if err != nil {
		t.Fatalf("ProviderStatusDetail failed: %v", err)
	}
	if detail.Auth.Status != AuthStatusSignedIn {
		t.Fatalf("expected AuthStatusSignedIn from hook, got %s", detail.Auth.Status)
	}
	if detail.Auth.Source != "test-hook" {
		t.Fatalf("expected test-hook source, got %s", detail.Auth.Source)
	}
}
