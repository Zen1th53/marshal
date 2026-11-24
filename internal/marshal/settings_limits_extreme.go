//go:build !governed

package marshal

import "errors"

const (
	DefaultInfraRetryLimit      = 6
	DefaultInfraFailoverStreak  = 2
	DefaultInfraCooldownSeconds = 600
)

type runtimeLimitSettings struct {
	// Optional pointers distinguish absent legacy values (use existing defaults)
	// from explicit zero (unlimited or disabled).
	InfraRetryLimit            *int `json:"infra_retry_limit,omitempty"`
	InfraFailoverStreak        *int `json:"infra_failover_streak,omitempty"`
	InfraCooldownSeconds       *int `json:"infra_cooldown_seconds,omitempty"`
	VerificationTimeoutSeconds *int `json:"verification_timeout_seconds,omitempty"`
}

func (s Settings) validateRuntimeLimitSettings() error {
	for _, p := range []*int{s.InfraRetryLimit, s.InfraFailoverStreak, s.InfraCooldownSeconds, s.VerificationTimeoutSeconds} {
		if p != nil && *p < 0 {
			return errors.New("invalid runtime limit setting")
		}
	}
	return nil
}

// EffectiveInfraRetryLimit returns the per-task infrastructure retry limit.
// Zero means unlimited; absent uses DefaultInfraRetryLimit.
func (s Settings) EffectiveInfraRetryLimit() int {
	if s.InfraRetryLimit != nil {
		return *s.InfraRetryLimit
	}
	return DefaultInfraRetryLimit
}

// EffectiveInfraFailoverStreak returns the consecutive failure streak before
// cooling a provider. Zero disables streak cooldown; absent uses DefaultInfraFailoverStreak.
func (s Settings) EffectiveInfraFailoverStreak() int {
	if s.InfraFailoverStreak != nil {
		return *s.InfraFailoverStreak
	}
	return DefaultInfraFailoverStreak
}

// EffectiveInfraCooldownSeconds returns the provider cooldown duration in seconds
// after an infrastructure failure streak. Zero disables cooldown; absent uses DefaultInfraCooldownSeconds.
func (s Settings) EffectiveInfraCooldownSeconds() int {
	if s.InfraCooldownSeconds != nil {
		return *s.InfraCooldownSeconds
	}
	return DefaultInfraCooldownSeconds
}

// EffectiveVerificationTimeoutSeconds returns the repository verification wall-clock
// limit in seconds. Zero means unlimited; absent uses the existing unlimited default.
func (s Settings) EffectiveVerificationTimeoutSeconds() int {
	if s.VerificationTimeoutSeconds != nil {
		return *s.VerificationTimeoutSeconds
	}
	return 0
}

// EffectiveEnabledFailoverOrder returns the failover order filtered to enabled providers.
func (s Settings) EffectiveEnabledFailoverOrder() []string {
	base := s.EffectiveFailoverOrder()
	if len(base) == 0 {
		base = DefaultProviderOrder()
	}
	var out []string
	for _, p := range base {
		if s.ProviderEnabled(p) {
			out = append(out, p)
		}
	}
	return out
}
