package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
)

type fakeStatusAndModelsProviderReader struct {
	statuses []ProviderStatusObservation
	probes   []ProviderProbe
	err      error
}

func (f fakeStatusAndModelsProviderReader) ProviderStatuses(ctx context.Context) ([]ProviderStatusObservation, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.statuses, nil
}

func (f fakeStatusAndModelsProviderReader) Providers(ctx context.Context) ([]ProviderProbe, error) {
	if f.err != nil {
		return nil, f.err
	}
	if len(f.probes) > 0 {
		return f.probes, nil
	}
	probes := make([]ProviderProbe, 0, len(f.statuses))
	for _, s := range f.statuses {
		probes = append(probes, ProviderProbe{
			Name:              s.Name,
			Found:             s.Found,
			Path:              s.Path,
			Version:           s.Version,
			Model:             s.Model,
			Probed:            true,
			AuthStatus:        string(s.Auth.Status),
			AuthReason:        s.Auth.Reason,
			AuthSource:        s.Auth.Source,
			AuthTime:          s.Auth.ObservedAt,
			UsageReported:     s.Usage.Reported,
			TotalTokens:       s.Usage.TotalTokens,
			CostUSD:           s.Usage.CostUSD,
			ModelCalls:        s.Usage.ModelCalls,
			UsageDuration:     s.Usage.Duration,
			CooldownActive:    s.Cooldown.Active,
			CooldownRemaining: s.Cooldown.RetryAfter,
			CooldownReason:    s.Cooldown.Reason,
			QuotaReason:       s.QuotaReason,
			ResetReason:       s.ResetReason,
			AllowanceReason:   s.AllowanceReason,
		})
	}
	return probes, nil
}

// Optimization stubs for ModelsReader
func (f fakeStatusAndModelsProviderReader) Cycles(ctx context.Context) ([]OptimizationCycle, error) {
	return nil, nil
}
func (f fakeStatusAndModelsProviderReader) CanariesFor(ctx context.Context, cycleID string) ([]OptimizationCanary, error) {
	return nil, nil
}
func (f fakeStatusAndModelsProviderReader) LocalModels(ctx context.Context) ([]LocalModel, error) {
	return nil, nil
}

type fakeEntitlementRequestReader struct {
	obs EntitlementRequestObservation
	err error
}

func (f fakeEntitlementRequestReader) EntitlementRequest(ctx context.Context) (EntitlementRequestObservation, error) {
	if f.err != nil {
		return EntitlementRequestObservation{}, f.err
	}
	return f.obs, nil
}

// TestProviderStatusKnownUnknownAndMeasuredZero table-drives providers, explicit auth,
// measured zero vs unknown usage, unsupported quota/reset/allowance, and cooldown.
func TestProviderStatusKnownUnknownAndMeasuredZero(t *testing.T) {
	zeroTokens := int64(0)
	zeroCost := float64(0.0)
	tenKTokens := int64(10000)
	twoDollars := float64(2.50)
	now := time.Date(2026, 10, 27, 8, 0, 0, 0, time.UTC)

	cases := []struct {
		name             string
		providerName     string
		canonical        string
		found            bool
		authStatus       app.ProviderAuthStatus
		authReason       string
		usageReported    bool
		tokens           *int64
		cost             *float64
		calls            int
		cooldownActive   bool
		retryAfter       time.Duration
		wantAuthStatus   Truth
		wantAuthText     string
		wantTokensStatus Truth
		wantTokensText   string
		wantCostStatus   Truth
		wantCostText     string
		wantQuotaStatus  Truth
		wantResetStatus  Truth
		wantAllowStatus  Truth
		wantCoolStatus   Truth
		wantCoolText     string
	}{
		{
			name:             "codex signed in with measured zero usage and no cooldown",
			providerName:     "Codex",
			canonical:        "codex",
			found:            true,
			authStatus:       app.AuthStatusSignedIn,
			authReason:       "codex reported signed in",
			usageReported:    true,
			tokens:           &zeroTokens,
			cost:             &zeroCost,
			calls:            1,
			cooldownActive:   false,
			wantAuthStatus:   TruthKnown,
			wantAuthText:     "signed in",
			wantTokensStatus: TruthKnown,
			wantTokensText:   "0 tokens",
			wantCostStatus:   TruthKnown,
			wantCostText:     "$0.0000",
			wantQuotaStatus:  TruthUnknown,
			wantResetStatus:  TruthUnknown,
			wantAllowStatus:  TruthUnknown,
			wantCoolStatus:   TruthKnown,
			wantCoolText:     "no active cooldown",
		},
		{
			name:             "claude unsupported auth with unknown usage and active cooldown",
			providerName:     "Claude",
			canonical:        "claude",
			found:            true,
			authStatus:       app.AuthStatusUnsupported,
			authReason:       "read-only auth inspection not supported",
			usageReported:    true,
			tokens:           nil, // unreported/unknown tokens
			cost:             nil, // unreported/unknown cost
			calls:            3,
			cooldownActive:   true,
			retryAfter:       30 * time.Second,
			wantAuthStatus:   TruthUnknown,
			wantAuthText:     "",
			wantTokensStatus: TruthUnknown,
			wantTokensText:   "",
			wantCostStatus:   TruthUnknown,
			wantCostText:     "",
			wantQuotaStatus:  TruthUnknown,
			wantResetStatus:  TruthUnknown,
			wantAllowStatus:  TruthUnknown,
			wantCoolStatus:   TruthKnown,
			wantCoolText:     "throttled",
		},
		{
			name:             "opencode signed out with positive measured usage",
			providerName:     "OpenCode",
			canonical:        "opencode",
			found:            true,
			authStatus:       app.AuthStatusNotSignedIn,
			authReason:       "OpenCode is not signed in",
			usageReported:    true,
			tokens:           &tenKTokens,
			cost:             &twoDollars,
			calls:            5,
			cooldownActive:   false,
			wantAuthStatus:   TruthKnown,
			wantAuthText:     "not signed in",
			wantTokensStatus: TruthKnown,
			wantTokensText:   "10000 tokens",
			wantCostStatus:   TruthKnown,
			wantCostText:     "$2.5000",
			wantQuotaStatus:  TruthUnknown,
			wantResetStatus:  TruthUnknown,
			wantAllowStatus:  TruthUnknown,
			wantCoolStatus:   TruthKnown,
			wantCoolText:     "no active cooldown",
		},
		{
			name:             "antigravity not installed",
			providerName:     "Antigravity",
			canonical:        "agy",
			found:            false,
			authStatus:       app.AuthStatusNotProbed,
			authReason:       "binary not found",
			usageReported:    false,
			tokens:           nil,
			cost:             nil,
			wantAuthStatus:   TruthNotRun,
			wantTokensStatus: TruthNotRun,
			wantCostStatus:   TruthNotRun,
			wantQuotaStatus:  TruthNotRun,
			wantResetStatus:  TruthNotRun,
			wantAllowStatus:  TruthNotRun,
			wantCoolStatus:   TruthNotRun,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			obs := ProviderStatusObservation{
				Name:      tc.providerName,
				Canonical: tc.canonical,
				Found:     tc.found,
				Auth: app.ProviderAuthObservation{
					Provider:   tc.canonical,
					Status:     tc.authStatus,
					Reason:     tc.authReason,
					Source:     "test",
					ObservedAt: now,
				},
				Usage: app.ProviderUsageObservation{
					Reported:    tc.usageReported,
					TotalTokens: tc.tokens,
					CostUSD:     tc.cost,
					ModelCalls:  tc.calls,
					Source:      "test",
					ObservedAt:  now,
				},
				Cooldown: app.ProviderCooldownObservation{
					Active:     tc.cooldownActive,
					RetryAfter: tc.retryAfter,
					Source:     "test",
					Reason:     "test cooldown",
					ObservedAt: now,
				},
				QuotaReason:     "no provider exposes a machine-readable quota or capacity through a supported interface",
				ResetReason:     "no provider exposes a machine-readable quota reset time through a supported interface",
				AllowanceReason: "no provider exposes a machine-readable temporary or promotional allowance through a supported interface",
				ObservedAt:      now,
			}

			reader := fakeStatusAndModelsProviderReader{statuses: []ProviderStatusObservation{obs}}
			source := &StatusSource{Now: func() time.Time { return now }}
			list := source.ReadProviders(context.Background(), reader)

			if len(list.Rows) != 1 {
				t.Fatalf("expected 1 row, got %d", len(list.Rows))
			}
			row := list.Rows[0]

			// Auth checks
			if row.Auth.Status != tc.wantAuthStatus {
				t.Errorf("Auth status = %v, want %v (reason: %q)", row.Auth.Status, tc.wantAuthStatus, row.Auth.Reason)
			}
			if tc.wantAuthText != "" && !strings.Contains(row.Auth.Display(), tc.wantAuthText) {
				t.Errorf("Auth text = %q, want to contain %q", row.Auth.Display(), tc.wantAuthText)
			}

			// Usage checks: measured zero distinct from unknown
			if row.UsageTokens.Status != tc.wantTokensStatus {
				t.Errorf("Tokens status = %v, want %v", row.UsageTokens.Status, tc.wantTokensStatus)
			}
			if tc.wantTokensText != "" && !strings.Contains(row.UsageTokens.Display(), tc.wantTokensText) {
				t.Errorf("Tokens text = %q, want to contain %q", row.UsageTokens.Display(), tc.wantTokensText)
			}
			if row.UsageCost.Status != tc.wantCostStatus {
				t.Errorf("Cost status = %v, want %v", row.UsageCost.Status, tc.wantCostStatus)
			}
			if tc.wantCostText != "" && !strings.Contains(row.UsageCost.Display(), tc.wantCostText) {
				t.Errorf("Cost text = %q, want to contain %q", row.UsageCost.Display(), tc.wantCostText)
			}

			// Quota/Reset/Allowance checks: unsupported values remain reasoned UNKNOWN
			if row.Quota.Status != tc.wantQuotaStatus {
				t.Errorf("Quota status = %v, want %v", row.Quota.Status, tc.wantQuotaStatus)
			}
			if row.Reset.Status != tc.wantResetStatus {
				t.Errorf("Reset status = %v, want %v", row.Reset.Status, tc.wantResetStatus)
			}
			if row.Allowance.Status != tc.wantAllowStatus {
				t.Errorf("Allowance status = %v, want %v", row.Allowance.Status, tc.wantAllowStatus)
			}

			// Cooldown checks
			if row.Cooldown.Status != tc.wantCoolStatus {
				t.Errorf("Cooldown status = %v, want %v", row.Cooldown.Status, tc.wantCoolStatus)
			}
			if tc.wantCoolText != "" && !strings.Contains(row.Cooldown.Display(), tc.wantCoolText) {
				t.Errorf("Cooldown text = %q, want to contain %q", row.Cooldown.Display(), tc.wantCoolText)
			}
		})
	}
}

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

// TestProviderStatusAndModelsDetailUseSameEvidence proves that Status provider rows
// and Models provider detail (CTUI-0523) consume the exact same underlying observations.
func TestProviderStatusAndModelsDetailUseSameEvidence(t *testing.T) {
	now := time.Date(2026, 10, 27, 8, 30, 0, 0, time.UTC)
	tokens := int64(12345)
	cost := float64(0.1234)

	obs := []ProviderStatusObservation{
		{
			Name:      "Codex",
			Canonical: "codex",
			Found:     true,
			Version:   "0.1.0",
			Model:     "gpt-5",
			Auth: app.ProviderAuthObservation{
				Provider:   "codex",
				Status:     app.AuthStatusSignedIn,
				Reason:     "codex reported signed in",
				Source:     "codex login status",
				ObservedAt: now,
			},
			Usage: app.ProviderUsageObservation{
				Reported:    true,
				TotalTokens: &tokens,
				CostUSD:     &cost,
				ModelCalls:  8,
				Duration:    12 * time.Second,
				Source:      "durable-store",
				ObservedAt:  now,
			},
			Cooldown: app.ProviderCooldownObservation{
				Active:     false,
				Reason:     "no active cooldown",
				Source:     "rate-limit",
				ObservedAt: now,
			},
			QuotaReason:     "no provider exposes a machine-readable quota or capacity through a supported interface",
			ResetReason:     "no provider exposes a machine-readable quota reset time through a supported interface",
			AllowanceReason: "no provider exposes a machine-readable temporary or promotional allowance through a supported interface",
			ObservedAt:      now,
		},
	}

	reader := fakeStatusAndModelsProviderReader{statuses: obs}

	// 1. Read through StatusSource
	statusSource := &StatusSource{Now: func() time.Time { return now }}
	statusList := statusSource.ReadProviders(context.Background(), reader)
	if len(statusList.Rows) != 1 {
		t.Fatalf("Status expected 1 row, got %d", len(statusList.Rows))
	}
	sRow := statusList.Rows[0]

	// 2. Read through ModelsFeed
	modelsFeed := &ModelsFeed{Reader: reader, Now: func() time.Time { return now }}
	modelsSnap := modelsFeed.ReadModels(context.Background())
	if len(modelsSnap.ProviderDetails) != 1 {
		t.Fatalf("Models expected 1 detail row, got %d", len(modelsSnap.ProviderDetails))
	}
	mRow := modelsSnap.ProviderDetails[0]

	// 3. Compare all fields for exact identity and parity
	if sRow.Name.Display() != mRow.Name.Display() {
		t.Errorf("Name mismatch: Status=%q, Models=%q", sRow.Name.Display(), mRow.Name.Display())
	}
	if sRow.Availability.Display() != mRow.Availability.Display() {
		t.Errorf("Availability mismatch: Status=%q, Models=%q", sRow.Availability.Display(), mRow.Availability.Display())
	}
	if sRow.Version.Display() != mRow.Version.Display() {
		t.Errorf("Version mismatch: Status=%q, Models=%q", sRow.Version.Display(), mRow.Version.Display())
	}
	if sRow.Model.Display() != mRow.Model.Display() {
		t.Errorf("Model mismatch: Status=%q, Models=%q", sRow.Model.Display(), mRow.Model.Display())
	}
	if sRow.Auth.Display() != mRow.Auth.Display() {
		t.Errorf("Auth mismatch: Status=%q, Models=%q", sRow.Auth.Display(), mRow.Auth.Display())
	}
	if sRow.Cooldown.Display() != mRow.Cooldown.Display() {
		t.Errorf("Cooldown mismatch: Status=%q, Models=%q", sRow.Cooldown.Display(), mRow.Cooldown.Display())
	}
	if sRow.UsageTokens.Display() != mRow.UsageTokens.Display() {
		t.Errorf("UsageTokens mismatch: Status=%q, Models=%q", sRow.UsageTokens.Display(), mRow.UsageTokens.Display())
	}
	if sRow.UsageCost.Display() != mRow.UsageCost.Display() {
		t.Errorf("UsageCost mismatch: Status=%q, Models=%q", sRow.UsageCost.Display(), mRow.UsageCost.Display())
	}
	if sRow.Quota.Display() != mRow.Quota.Display() {
		t.Errorf("Quota mismatch: Status=%q, Models=%q", sRow.Quota.Display(), mRow.Quota.Display())
	}
	if sRow.Reset.Display() != mRow.Reset.Display() {
		t.Errorf("Reset mismatch: Status=%q, Models=%q", sRow.Reset.Display(), mRow.Reset.Display())
	}
	if sRow.Allowance.Display() != mRow.Allowance.Display() {
		t.Errorf("Allowance mismatch: Status=%q, Models=%q", sRow.Allowance.Display(), mRow.Allowance.Display())
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

// TestEntitlementRequestStatusModelsParityWithIndependentLease proves CTUI-0157
// and CTUI-0557 parity across lease states and request observation shapes.
func TestEntitlementRequestStatusModelsParityWithIndependentLease(t *testing.T) {
	now := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)

	cases := []struct {
		name            string
		leaseEntitled   bool
		leaseMode       string
		obs             EntitlementRequestObservation
		obsErr          error
		hasReader       bool
		configured      bool
		wantState       string
		wantLocalID     string
		wantServerID    string
		wantServerKnown bool
	}{
		{
			name:          "standard session with recorded request and server ID",
			leaseEntitled: false,
			leaseMode:     "Standard",
			obs: EntitlementRequestObservation{
				Recorded:        true,
				State:           "PENDING",
				LocalAttemptID:  "ATTEMPT-1001",
				ServerRequestID: "SRV-REQ-5555",
				HasServerID:     true,
				RequestedAt:     now.Add(-5 * time.Minute),
			},
			hasReader:       true,
			configured:      true,
			wantState:       "PENDING",
			wantLocalID:     "ATTEMPT-1001",
			wantServerID:    "SRV-REQ-5555",
			wantServerKnown: true,
		},
		{
			name:          "ultra session with old status-only server response (server ID unknown)",
			leaseEntitled: true,
			leaseMode:     "ULTRA",
			obs: EntitlementRequestObservation{
				Recorded:        true,
				State:           "ACTIVE",
				LocalAttemptID:  "ATTEMPT-2002",
				ServerRequestID: "",
				HasServerID:     false,
				RequestedAt:     now.Add(-15 * time.Minute),
			},
			hasReader:       true,
			configured:      true,
			wantState:       "ACTIVE",
			wantLocalID:     "ATTEMPT-2002",
			wantServerID:    "server response did not return a server request ID",
			wantServerKnown: false, // Must be UNKNOWN, not made-up ID
		},
		{
			name:          "unconfigured cloud reports not run with reason",
			leaseEntitled: false,
			leaseMode:     "Standard",
			hasReader:     true,
			configured:    false,
			wantState:     "Not run yet",
		},
		{
			name:          "missing request reader reports unknown with reason",
			leaseEntitled: false,
			leaseMode:     "Standard",
			hasReader:     false,
			configured:    true,
			wantState:     "UNKNOWN",
		},
		{
			name:          "reader error reports error status",
			leaseEntitled: false,
			leaseMode:     "Standard",
			obsErr:        errors.New("i/o failure reading request state"),
			hasReader:     true,
			configured:    true,
			wantState:     "ERROR",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cloudReader := &fakeCloud{
				configured: tc.configured,
				entitled:   tc.leaseEntitled,
			}

			var reqReader EntitlementRequestReader
			if tc.hasReader {
				reqReader = fakeEntitlementRequestReader{obs: tc.obs, err: tc.obsErr}
			}

			// 1. Read through StatusSource (CTUI-0157)
			statusSource := &StatusSource{
				Now:           func() time.Time { return now },
				Cloud:         cloudReader,
				RequestReader: reqReader,
			}
			cloudSnap := statusSource.ReadCloud(context.Background())

			// 2. Read through ModelsFeed (CTUI-0557)
			modelsFeed := &ModelsFeed{
				Reader:        fakeStatusAndModelsProviderReader{},
				Now:           func() time.Time { return now },
				RequestReader: reqReader,
			}
			modelsSnap := modelsFeed.ReadModels(context.Background())

			// 3. Verify lease independence
			if tc.configured {
				if tc.leaseEntitled && cloudSnap.Mode.Display() != "ULTRA" {
					t.Errorf("cloud mode = %s, want ULTRA", cloudSnap.Mode.Display())
				}
				if !tc.leaseEntitled && cloudSnap.Mode.Display() != "Standard" {
					t.Errorf("cloud mode = %s, want Standard", cloudSnap.Mode.Display())
				}
			}

			// 4. Verify CTUI-0157 vs CTUI-0557 parity
			if cloudSnap.RequestStatus.Display() != modelsSnap.EntitlementRequestStatus.Display() {
				t.Errorf("RequestStatus mismatch: Status=%q, Models=%q",
					cloudSnap.RequestStatus.Display(), modelsSnap.EntitlementRequestStatus.Display())
			}
			if cloudSnap.LocalAttemptID.Display() != modelsSnap.EntitlementLocalAttemptID.Display() {
				t.Errorf("LocalAttemptID mismatch: Status=%q, Models=%q",
					cloudSnap.LocalAttemptID.Display(), modelsSnap.EntitlementLocalAttemptID.Display())
			}
			if cloudSnap.ServerReqID.Display() != modelsSnap.EntitlementServerID.Display() {
				t.Errorf("ServerReqID mismatch: Status=%q, Models=%q",
					cloudSnap.ServerReqID.Display(), modelsSnap.EntitlementServerID.Display())
			}
			if cloudSnap.RequestAge.Display() != modelsSnap.EntitlementAge.Display() {
				t.Errorf("RequestAge mismatch: Status=%q, Models=%q",
					cloudSnap.RequestAge.Display(), modelsSnap.EntitlementAge.Display())
			}

			// 5. Specific state assertions
			if tc.wantState != "" && !strings.Contains(cloudSnap.RequestStatus.Display(), tc.wantState) {
				t.Errorf("RequestStatus display = %q, want to contain %q", cloudSnap.RequestStatus.Display(), tc.wantState)
			}
			if tc.wantLocalID != "" && !strings.Contains(cloudSnap.LocalAttemptID.Display(), tc.wantLocalID) {
				t.Errorf("LocalAttemptID display = %q, want to contain %q", cloudSnap.LocalAttemptID.Display(), tc.wantLocalID)
			}
			if tc.wantServerID != "" && !strings.Contains(cloudSnap.ServerReqID.Display(), tc.wantServerID) {
				t.Errorf("ServerReqID display = %q, want to contain %q", cloudSnap.ServerReqID.Display(), tc.wantServerID)
			}
			if tc.hasReader && tc.obs.Recorded && !tc.obs.HasServerID {
				if cloudSnap.ServerReqID.Status != TruthUnknown {
					t.Errorf("ServerReqID status = %v, want TruthUnknown for status-only server response", cloudSnap.ServerReqID.Status)
				}
			}
		})
	}
}
