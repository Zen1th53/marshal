//go:build !governed

package marshal

import (
	"fmt"
	"strings"
)

type failoverSettings struct {
	FailoverOrder string `json:"failover_order,omitempty"`
}
type failoverTask struct {
	ProviderFailoverPending bool   `json:",omitempty"`
	ProviderFailovers       int    `json:",omitempty"`
	FailoverBrief           string `json:",omitempty"`
	// LaneReworkBase counts returns retired by a reassignment or retry, so a
	// task's lane numbering never goes back to an earlier attempt.
	LaneReworkBase int `json:",omitempty"`
	// InfraRetries counts attempts that ended without a hand-in for an
	// infrastructure, provider, transport or tool failure. They are not
	// rework and have a bound of their own.
	InfraRetries int `json:",omitempty"`
	// InfraStreakWorker and InfraStreak count consecutive such failures of
	// one provider on the task; a streak cools the provider for failover.
	InfraStreakWorker string `json:",omitempty"`
	InfraStreak       int    `json:",omitempty"`
}

// EffectiveFailoverOrder returns parsed failover providers from the settings.
func (s Settings) EffectiveFailoverOrder() []string {
	if s.FailoverOrder == "" {
		return nil
	}
	parts := strings.Split(s.FailoverOrder, ",")
	var out []string
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func (s Settings) validateFailoverSettings() error {
	if s.FailoverOrder != "" && len(s.EffectiveFailoverOrder()) == 0 {
		return fmt.Errorf("failover order cannot be empty")
	}
	seen := map[string]bool{}
	for _, p := range s.EffectiveFailoverOrder() {
		switch p {
		case "codex", "agy", "claude", "opencode":
		default:
			return fmt.Errorf("invalid failover provider: %s", p)
		}
		if seen[p] {
			return fmt.Errorf("duplicate failover provider: %s", p)
		}
		seen[p] = true
	}
	return nil
}
