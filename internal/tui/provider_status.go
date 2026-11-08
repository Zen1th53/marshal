package tui

import (
	"context"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
)

// ProviderStatusObservation is the TUI's typed view of canonical provider status evidence.
type ProviderStatusObservation struct {
	Name            string
	Canonical       string
	Found           bool
	Path            string
	Version         string
	Model           string
	Auth            app.ProviderAuthObservation
	Usage           app.ProviderUsageObservation
	Cooldown        app.ProviderCooldownObservation
	QuotaReason     string
	ResetReason     string
	AllowanceReason string
	ObservedAt      time.Time
}

// ProviderStatusReader is an optional richer interface implemented by authority adapters.
type ProviderStatusReader interface {
	ProviderStatuses(ctx context.Context) ([]ProviderStatusObservation, error)
}

// EntitlementRequestObservation represents the persisted local entitlement request state.
// Local attempt ID and observed server ID are kept distinct; an old status-only
// server response gets server-ID UNKNOWN, never a fabricated ID.
type EntitlementRequestObservation struct {
	LocalAttemptID  string    `json:"local_attempt_id"`
	ServerRequestID string    `json:"server_request_id,omitempty"`
	HasServerID     bool      `json:"has_server_id"`
	State           string    `json:"state"`
	RequestedAt     time.Time `json:"requested_at"`
	UpdatedAt       time.Time `json:"updated_at,omitempty"`
	Error           string    `json:"error,omitempty"`
	Endpoint        string    `json:"endpoint,omitempty"`
	InstallationID  string    `json:"installation_id,omitempty"`
	SessionID       string    `json:"session_id,omitempty"`
	Recorded        bool      `json:"recorded"`
}

// EntitlementRequestReader is an optional reader that reads persisted entitlement requests.
type EntitlementRequestReader interface {
	EntitlementRequest(ctx context.Context) (EntitlementRequestObservation, error)
}

// ProviderStatuses adapts the canonical runtime provider status details.
func (a *runtimeControlAuthority) ProviderStatuses(ctx context.Context) ([]ProviderStatusObservation, error) {
	if a == nil || a.runtime == nil {
		return nil, errNoRuntime
	}
	details, err := a.runtime.ProviderStatusDetails(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ProviderStatusObservation, 0, len(details))
	for _, d := range details {
		out = append(out, ProviderStatusObservation{
			Name:            d.Name,
			Canonical:       d.Canonical,
			Found:           d.Found,
			Path:            d.Path,
			Version:         d.Version,
			Model:           d.Model,
			Auth:            d.Auth,
			Usage:           d.Usage,
			Cooldown:        d.Cooldown,
			QuotaReason:     d.QuotaReason,
			ResetReason:     d.ResetReason,
			AllowanceReason: d.AllowanceReason,
			ObservedAt:      d.ObservedAt,
		})
	}
	return out, nil
}

func formatAuthValue(auth app.ProviderAuthObservation, found bool, now time.Time) Value {
	const source = "internal/app/provider_status.go, internal/tui/auth_preflight.go"
	if !found {
		return NotRun("provider executable was not found on PATH", source)
	}

	switch auth.Status {
	case app.AuthStatusSignedIn:
		reason := auth.Reason
		if reason == "" {
			reason = "provider reported signed in"
		}
		return Value{
			Text:   "signed in",
			Status: TruthKnown,
			Reason: reason,
			Source: auth.Source,
		}
	case app.AuthStatusNotSignedIn:
		reason := auth.Reason
		if reason == "" {
			reason = "provider is not signed in"
		}
		return Value{
			Text:   "not signed in",
			Status: TruthKnown,
			Reason: reason,
			Source: auth.Source,
		}
	case app.AuthStatusSkipped:
		reason := auth.Reason
		if reason == "" {
			reason = "auth probe was skipped for this command or session"
		}
		return Unknown(reason, source)
	case app.AuthStatusStale:
		reason := auth.Reason
		if reason == "" {
			reason = "auth observation is stale; reprobe required"
		}
		return Unknown(reason, source)
	case app.AuthStatusTimeout:
		reason := auth.Reason
		if reason == "" {
			reason = "auth probe timed out"
		}
		return Errored(reason, source)
	case app.AuthStatusError:
		reason := auth.Reason
		if reason == "" {
			reason = "auth probe encountered an error"
		}
		return Errored("auth probe failed: "+RedactContent(reason, nil), source)
	case app.AuthStatusUnsupported:
		reason := auth.Reason
		if reason == "" {
			reason = "read-only authentication state inspection is not supported by this provider CLI"
		}
		return Unknown(reason, source)
	case app.AuthStatusNotProbed:
		reason := auth.Reason
		if reason == "" {
			reason = "authentication status has not been probed in this session"
		}
		return NotRun(reason, source)
	default:
		return Unknown("authentication status has no inspectable evidence", source)
	}
}

func formatTokensValue(tokens *int64, reported bool, source string) Value {
	if source == "" {
		source = "internal/adapter/adapter.go, internal/store/budget.go"
	}
	if !reported {
		return NotRun("no task execution recorded in this session", source)
	}
	if tokens == nil {
		return Unknown("token consumption was not reported by provider", source)
	}
	// Measured zero distinct from unknown
	return Known(fmt.Sprintf("%d tokens", *tokens), source)
}

func formatCostValue(cost *float64, reported bool, source string) Value {
	if source == "" {
		source = "internal/adapter/adapter.go, internal/store/budget.go"
	}
	if !reported {
		return NotRun("no task execution recorded in this session", source)
	}
	if cost == nil {
		return Unknown("cost was not reported by provider", source)
	}
	// Measured zero distinct from unknown
	return Known(fmt.Sprintf("$%.4f", *cost), source)
}

func formatCallsValue(calls int, reported bool, source string) Value {
	if source == "" {
		source = "internal/adapter/adapter.go, internal/store/budget.go"
	}
	if !reported {
		return NotRun("no task execution recorded in this session", source)
	}
	return Known(fmt.Sprintf("%d calls", calls), source)
}

func formatDurationValue(d time.Duration, reported bool, source string) Value {
	if source == "" {
		source = "internal/adapter/adapter.go, internal/store/budget.go"
	}
	if !reported {
		return NotRun("no task execution recorded in this session", source)
	}
	return Known(roundDuration(d), source)
}

func formatCooldownValue(cd app.ProviderCooldownObservation, found bool, now time.Time) Value {
	const source = "internal/execution/rate_limit.go"
	if !found {
		return NotRun("provider executable was not found on PATH", source)
	}
	if cd.Active {
		dur := cd.RetryAfter
		if !cd.ResetTime.IsZero() && cd.ResetTime.After(now) {
			dur = cd.ResetTime.Sub(now)
		}
		reason := cd.Reason
		if reason == "" {
			reason = "active rate limit cooldown"
		}
		return Value{
			Text:   fmt.Sprintf("throttled (retry in %s)", roundDuration(dur)),
			Status: TruthKnown,
			Reason: reason,
			Source: source,
		}
	}
	return Known("no active cooldown", source)
}
