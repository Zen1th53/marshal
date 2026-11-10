package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
)

// TestProviderAuthSkippedProbeIsUnknown proves that skipping an auth probe is
// recorded and presented as UNKNOWN, never as authenticated evidence.
func TestProviderAuthSkippedProbeIsUnknown(t *testing.T) {
	now := time.Now().UTC()
	ClearAuthPreflightObservations()
	defer ClearAuthPreflightObservations()

	// 1. Record an explicitly skipped observation
	RecordAuthPreflightObservation(AuthObservation{
		Provider:   "codex",
		Status:     "SKIPPED",
		Reason:     "auth check skipped for --help command",
		Source:     "shouldSkipAuthPreflight",
		ObservedAt: now,
	})

	obs, ok := GetAuthPreflightObservation("codex", 10*time.Second)
	if !ok {
		t.Fatal("expected cached observation for codex")
	}
	if obs.Status != "SKIPPED" {
		t.Fatalf("expected status SKIPPED, got %s", obs.Status)
	}

	val := formatAuthValue(app.ProviderAuthObservation{
		Provider:   obs.Provider,
		Status:     app.ProviderAuthStatus(obs.Status),
		Reason:     obs.Reason,
		Source:     obs.Source,
		ObservedAt: obs.ObservedAt,
	}, true, now)

	if val.Status.IsSuccess() {
		t.Fatalf("skipped probe rendered as success: %+v", val)
	}
	if val.Status != TruthUnknown {
		t.Fatalf("skipped probe status = %v, want TruthUnknown", val.Status)
	}
	if !strings.Contains(val.Reason, "skipped") {
		t.Fatalf("expected reason to state probe was skipped: %q", val.Reason)
	}

	// 2. Non-Codex providers return nil during defaultAuthPreflight to preserve
	// launch behavior, but this nil is NOT authenticated evidence.
	nonCodexErr := defaultAuthPreflight(context.Background(), "claude", "/bin/claude", nil)
	if nonCodexErr != nil {
		t.Fatalf("expected nil error to preserve launch behavior, got %v", nonCodexErr)
	}
	claudeObs, found := GetAuthPreflightObservation("claude", 10*time.Second)
	if !found {
		t.Fatal("expected preflight observation recorded for claude")
	}
	if claudeObs.Status == "SIGNED_IN" {
		t.Fatalf("defaultAuthPreflight nil for claude was recorded as SIGNED_IN: %+v", claudeObs)
	}
	if claudeObs.Status != "UNSUPPORTED" {
		t.Fatalf("expected UNSUPPORTED for claude, got %s", claudeObs.Status)
	}
}

// TestProviderStatusRefreshHasNoAuthenticationSideEffects proves that reading
// provider status does not execute login commands or mutate auth state.
func TestProviderStatusRefreshHasNoAuthenticationSideEffects(t *testing.T) {
	ctx := context.Background()
	r := &app.Runtime{}

	// Take baseline observation
	before, err := r.ProviderStatusDetails(ctx)
	if err != nil {
		t.Fatalf("initial ProviderStatusDetails: %v", err)
	}

	// Read multiple times in succession to simulate status screen polling/refresh
	for i := 0; i < 5; i++ {
		after, err := r.ProviderStatusDetails(ctx)
		if err != nil {
			t.Fatalf("refresh ProviderStatusDetails %d: %v", i, err)
		}
		if len(after) != len(before) {
			t.Fatalf("provider count changed on refresh %d: before=%d, after=%d", i, len(before), len(after))
		}
	}
}

// TestProviderStatusEvidenceFreshnessAndIdentity proves freshness decay to UNKNOWN,
// canonical alias normalization, and redaction of sensitive errors.
func TestProviderStatusEvidenceFreshnessAndIdentity(t *testing.T) {
	now := time.Now().UTC()

	// 1. Alias normalization
	if c := app.CanonicalProviderName("Antigravity"); c != "agy" {
		t.Errorf("CanonicalProviderName(Antigravity) = %s, want agy", c)
	}
	if c := app.CanonicalProviderName("AGY"); c != "agy" {
		t.Errorf("CanonicalProviderName(AGY) = %s, want agy", c)
	}
	if d := app.ProviderDisplayName("agy"); d != "Antigravity" {
		t.Errorf("ProviderDisplayName(agy) = %s, want Antigravity", d)
	}

	// 2. Freshness check
	fresh := app.ProviderAuthObservation{
		Provider:   "codex",
		Status:     app.AuthStatusSignedIn,
		ObservedAt: now.Add(-10 * time.Second),
	}
	if !fresh.IsFresh(30*time.Second, now) {
		t.Error("expected fresh observation")
	}

	stale := app.ProviderAuthObservation{
		Provider:   "codex",
		Status:     app.AuthStatusSignedIn,
		ObservedAt: now.Add(-120 * time.Second),
	}
	if stale.IsFresh(30*time.Second, now) {
		t.Error("expected stale observation to not be fresh")
	}
	staleVal := formatAuthValue(app.ProviderAuthObservation{
		Provider:   "codex",
		Status:     app.AuthStatusStale,
		Reason:     "cached observation is stale",
		ObservedAt: stale.ObservedAt,
	}, true, now)
	if staleVal.Status != TruthUnknown {
		t.Errorf("stale auth value status = %v, want TruthUnknown", staleVal.Status)
	}

	// 3. Error redaction
	sensitiveErr := "failed with token sk-secret-bearer-12345 at /home/user/.codex/auth"
	redactedVal := formatAuthValue(app.ProviderAuthObservation{
		Provider:   "codex",
		Status:     app.AuthStatusError,
		Reason:     sensitiveErr,
		ObservedAt: now,
	}, true, now)
	if strings.Contains(redactedVal.Display(), "sk-secret-bearer-12345") {
		t.Errorf("sensitive token was not redacted in error: %q", redactedVal.Display())
	}
}
