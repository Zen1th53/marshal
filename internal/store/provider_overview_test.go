package store

import (
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestProviderOverviewNoProfiles(t *testing.T) {
	s := openTestStore(t)
	marshalTestProject(t, s)
	ctx := t.Context()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

	// No profiles, no run: empty result
	got, err := s.ProviderOverview(ctx, "p", now)
	if err != nil {
		t.Fatalf("ProviderOverview failed: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 providers, got %d", len(got))
	}
}

func TestProviderOverviewFreshAndExpiredProfiles(t *testing.T) {
	s := openTestStore(t)
	marshalTestProject(t, s)
	ctx := t.Context()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

	freshProfile := model.HarnessProfile{
		Harness:          "codex",
		InstalledVersion: "0.28.0",
		BinaryPath:       "codex",
		SupportedModels:  []string{"gpt-4o"},
		DefaultModel:     "gpt-4o",
		ProbeEvidenceID:  "ev-fresh",
		ProbedAt:         now.Add(-2 * time.Hour),
		ExpiresAt:        now.Add(22 * time.Hour),
	}
	if err := s.SaveHarnessProfile(ctx, freshProfile); err != nil {
		t.Fatalf("SaveHarnessProfile(codex) failed: %v", err)
	}

	expiredProfile := model.HarnessProfile{
		Harness:          "claude-code",
		InstalledVersion: "1.0.12",
		BinaryPath:       "claude",
		SupportedModels:  []string{"claude-3-7-sonnet"},
		DefaultModel:     "claude-3-7-sonnet",
		ProbeEvidenceID:  "ev-expired",
		ProbedAt:         now.Add(-48 * time.Hour),
		ExpiresAt:        now.Add(-24 * time.Hour),
	}
	if err := s.SaveHarnessProfile(ctx, expiredProfile); err != nil {
		t.Fatalf("SaveHarnessProfile(claude-code) failed: %v", err)
	}

	got, err := s.ProviderOverview(ctx, "p", now)
	if err != nil {
		t.Fatalf("ProviderOverview failed: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(got))
	}

	// Alphabetically sorted: "claude-code", then "codex"
	if got[0].Provider != "claude-code" {
		t.Errorf("expected first provider 'claude-code', got %q", got[0].Provider)
	}
	if got[0].Model != "claude-3-7-sonnet" {
		t.Errorf("expected model 'claude-3-7-sonnet', got %q", got[0].Model)
	}
	if !got[0].Expired {
		t.Errorf("expected claude-code to be expired")
	}
	if !got[0].VerifiedAt.Equal(expiredProfile.ProbedAt) {
		t.Errorf("expected verified_at %v, got %v", expiredProfile.ProbedAt, got[0].VerifiedAt)
	}
	if !got[0].ExpiresAt.Equal(expiredProfile.ExpiresAt) {
		t.Errorf("expected expires_at %v, got %v", expiredProfile.ExpiresAt, got[0].ExpiresAt)
	}

	if got[1].Provider != "codex" {
		t.Errorf("expected second provider 'codex', got %q", got[1].Provider)
	}
	if got[1].Model != "gpt-4o" {
		t.Errorf("expected model 'gpt-4o', got %q", got[1].Model)
	}
	if got[1].Expired {
		t.Errorf("expected codex to be fresh")
	}
	if !got[1].VerifiedAt.Equal(freshProfile.ProbedAt) {
		t.Errorf("expected verified_at %v, got %v", freshProfile.ProbedAt, got[1].VerifiedAt)
	}
	if !got[1].ExpiresAt.Equal(freshProfile.ExpiresAt) {
		t.Errorf("expected expires_at %v, got %v", freshProfile.ExpiresAt, got[1].ExpiresAt)
	}
}

func TestProviderOverviewLimitEventNewerThanProfile(t *testing.T) {
	s := openTestStore(t)
	marshalTestProject(t, s)
	ctx := t.Context()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

	profile := model.HarnessProfile{
		Harness:          "antigravity",
		InstalledVersion: "2.1.0",
		BinaryPath:       "agy",
		SupportedModels:  []string{"gemini-2.5-pro"},
		DefaultModel:     "gemini-2.5-pro",
		ProbeEvidenceID:  "ev-agy",
		ProbedAt:         now.Add(-4 * time.Hour),
		ExpiresAt:        now.Add(20 * time.Hour),
	}
	if err := s.SaveHarnessProfile(ctx, profile); err != nil {
		t.Fatalf("SaveHarnessProfile(antigravity) failed: %v", err)
	}

	limitTime := now.Add(-1 * time.Hour)
	if err := s.AppendEvent(ctx, nil, model.Event{
		ID:        "evt-limit-1",
		Type:      "provider.cooldown",
		Timestamp: limitTime,
		Data:      map[string]any{"provider": "antigravity", "reason": "provider rate limit exceeded"},
	}); err != nil {
		t.Fatalf("AppendEvent failed: %v", err)
	}

	got, err := s.ProviderOverview(ctx, "p", now)
	if err != nil {
		t.Fatalf("ProviderOverview failed: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(got))
	}
	if got[0].Provider != "antigravity" {
		t.Fatalf("expected provider 'antigravity', got %q", got[0].Provider)
	}
	if got[0].LastLimitReason != "provider rate limit exceeded" {
		t.Errorf("expected LastLimitReason 'provider rate limit exceeded', got %q", got[0].LastLimitReason)
	}
	if !got[0].LastLimitAt.Equal(limitTime) {
		t.Errorf("expected LastLimitAt %v, got %v", limitTime, got[0].LastLimitAt)
	}
	if !got[0].LimitUntil.IsZero() {
		t.Errorf("a cooldown without a reset time has LimitUntil %v", got[0].LimitUntil)
	}
	until := now.Add(2 * time.Hour)
	if err := s.AppendEvent(ctx, nil, model.Event{
		ID:        "evt-limit-2",
		Type:      "provider.cooldown",
		Timestamp: now.Add(-time.Minute),
		Data:      map[string]any{"provider": "antigravity", "reason": "usage limit", "reset_time": until.Format(time.RFC3339Nano)},
	}); err != nil {
		t.Fatalf("AppendEvent failed: %v", err)
	}
	again, err := s.ProviderOverview(ctx, "p", now)
	if err != nil {
		t.Fatalf("ProviderOverview failed: %v", err)
	}
	if !again[0].LimitUntil.Equal(until) || again[0].LastLimitReason != "usage limit" {
		t.Errorf("newest cooldown: until %v reason %q", again[0].LimitUntil, again[0].LastLimitReason)
	}
	if !got[0].LastLimitAt.After(got[0].VerifiedAt) {
		t.Errorf("expected LastLimitAt (%v) to be newer than VerifiedAt (%v)", got[0].LastLimitAt, got[0].VerifiedAt)
	}
}

func TestProviderOverviewLanesCountedFromFixtureRun(t *testing.T) {
	s := openTestStore(t)
	marshalTestProject(t, s)
	ctx := t.Context()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

	// One profile exists for codex, none for opencode or claude
	codexProfile := model.HarnessProfile{
		Harness:          "codex",
		InstalledVersion: "0.28.0",
		BinaryPath:       "codex",
		SupportedModels:  []string{"gpt-4o"},
		DefaultModel:     "gpt-4o",
		ProbeEvidenceID:  "ev-codex",
		ProbedAt:         now.Add(-2 * time.Hour),
		ExpiresAt:        now.Add(22 * time.Hour),
	}
	if err := s.SaveHarnessProfile(ctx, codexProfile); err != nil {
		t.Fatalf("SaveHarnessProfile failed: %v", err)
	}

	// Create fixture run with various task states
	tasks := []marshal.Task{
		{PlanTaskID: "t1", Worker: "codex", State: marshal.Dispatched},
		{PlanTaskID: "t2", Worker: "codex", State: marshal.HandedIn},
		{PlanTaskID: "t3", Worker: "codex", State: marshal.Merged}, // Not active
		{PlanTaskID: "t4", Worker: "opencode", State: marshal.Returned},
		{PlanTaskID: "t5", Worker: "opencode", State: marshal.Escalated},
		{PlanTaskID: "t6", Worker: "claude", State: marshal.Queued}, // Not active
	}

	run := marshal.Run{
		PlanID:              "plan-1",
		PlanVersion:         1,
		ApprovalScopeDigest: "digest-1",
		BaseCommit:          "commit-base",
		Tier:                marshal.Standard,
		Settings:            marshal.DefaultSettings(),
		Tasks:               tasks,
		State:               marshal.Dispatching,
	}

	if _, err := s.SetMarshalRun(ctx, "p", "run-1", run, 0); err != nil {
		t.Fatalf("SetMarshalRun failed: %v", err)
	}

	got, err := s.ProviderOverview(ctx, "p", now)
	if err != nil {
		t.Fatalf("ProviderOverview failed: %v", err)
	}

	// Expect 3 providers: claude (0 active lanes, no profile), codex (2 active lanes, profile), opencode (2 active lanes, no profile)
	if len(got) != 3 {
		t.Fatalf("expected 3 providers, got %d", len(got))
	}

	// Sorted: claude, codex, opencode
	if got[0].Provider != "claude" {
		t.Errorf("got[0].Provider = %q, want 'claude'", got[0].Provider)
	}
	if got[0].ActiveLanes != 0 {
		t.Errorf("got[0].ActiveLanes = %d, want 0", got[0].ActiveLanes)
	}
	if got[0].Model != "" || !got[0].VerifiedAt.IsZero() {
		t.Errorf("got[0] expected empty profile fields, got %+v", got[0])
	}

	if got[1].Provider != "codex" {
		t.Errorf("got[1].Provider = %q, want 'codex'", got[1].Provider)
	}
	if got[1].ActiveLanes != 1 {
		t.Errorf("got[1].ActiveLanes = %d, want 1 (only the dispatched task holds a lane)", got[1].ActiveLanes)
	}
	if got[1].Model != "gpt-4o" {
		t.Errorf("got[1].Model = %q, want 'gpt-4o'", got[1].Model)
	}

	if got[2].Provider != "opencode" {
		t.Errorf("got[2].Provider = %q, want 'opencode'", got[2].Provider)
	}
	if got[2].ActiveLanes != 0 {
		t.Errorf("got[2].ActiveLanes = %d, want 0 (returned and escalated tasks hold no lane)", got[2].ActiveLanes)
	}
	if got[2].Model != "" || !got[2].VerifiedAt.IsZero() {
		t.Errorf("got[2] expected empty profile fields, got %+v", got[2])
	}
}
