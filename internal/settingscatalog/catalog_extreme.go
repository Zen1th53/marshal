//go:build !governed

package settingscatalog

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Zen1th53/marshal/internal/marshal"
)

func optIntField(key, units, def, applies, help string, ptr func(*marshal.Settings) **int) Descriptor {
	d := Descriptor{
		Key:      key,
		Label:    key,
		Kind:     "integer",
		Units:    units,
		Default:  def,
		Editions: []string{"default"},
		Scope:    "project",
		Applies:  applies,
		Help:     help,
	}
	d.get = func(s marshal.Settings) string {
		p := *ptr(&s)
		if p == nil {
			return ""
		}
		return strconv.Itoa(*p)
	}
	d.set = func(s *marshal.Settings, raw string) error {
		trimmed := strings.ToLower(strings.TrimSpace(raw))
		if trimmed == "" || trimmed == "default" {
			*ptr(s) = nil
			return nil
		}
		n, err := strconv.Atoi(trimmed)
		if err != nil || n < 0 {
			return fmt.Errorf("%s needs a non-negative number (%s)", key, units)
		}
		*ptr(s) = &n
		return nil
	}
	return d
}

func editionRows() []Descriptor {
	rows := []Descriptor{
		field("codex-limit", "integer", "workers", nil, func(s *marshal.Settings) *int { return &s.ProviderLimits.Codex }),
		field("claude-limit", "integer", "workers", nil, func(s *marshal.Settings) *int { return &s.ProviderLimits.Claude }),
		field("agy-limit", "integer", "workers", nil, func(s *marshal.Settings) *int { return &s.ProviderLimits.Agy }),
		field("opencode-limit", "integer", "workers", nil, func(s *marshal.Settings) *int { return &s.ProviderLimits.OpenCode }),
		field("global-cap", "integer", "workers", nil, func(s *marshal.Settings) *int { return &s.GlobalCap }),
		field("failover-order", "string", "", nil, func(s *marshal.Settings) *string { return &s.FailoverOrder }),
	}
	for i := range rows {
		rows[i].Editions = []string{"default"}
	}
	d := &rows[len(rows)-1]
	d.Applies = "next admission"
	d.Help = "Controls provider failover order for new dispatches, reassignments and quota failovers."
	d.set = func(s *marshal.Settings, raw string) error {
		// An empty value restores the default order; separators alone do not.
		blank := strings.TrimSpace(raw) == ""
		raw = strings.NewReplacer("->", ",", " ", ",", "\t", ",").Replace(strings.ToLower(raw))
		parts := []string{}
		seen := map[string]bool{}
		for _, p := range strings.Split(raw, ",") {
			if p == "" {
				continue
			}
			if seen[p] {
				return fmt.Errorf("duplicate provider %q in failover order", p)
			}
			seen[p] = true
			parts = append(parts, p)
		}
		if len(parts) == 0 && !blank {
			return errors.New("failover order cannot be empty")
		}
		s.FailoverOrder = strings.Join(parts, ",")
		return nil
	}
	rows = append(rows,
		optIntField("infra-retry-limit", "attempts", strconv.Itoa(marshal.DefaultInfraRetryLimit), "next run",
			"Approved and running settings keep their snapshot. Zero makes infrastructure retries unlimited; blank restores 6.",
			func(s *marshal.Settings) **int { return &s.InfraRetryLimit }),
		optIntField("infra-failover-streak", "failures", strconv.Itoa(marshal.DefaultInfraFailoverStreak), "next run",
			"Approved and running settings keep their snapshot. Zero disables failure-streak cooldown; blank restores 2.",
			func(s *marshal.Settings) **int { return &s.InfraFailoverStreak }),
		optIntField("infra-cooldown-seconds", "seconds", strconv.Itoa(marshal.DefaultInfraCooldownSeconds), "next run",
			"Approved and running settings keep their snapshot. Zero disables provider cooldown after failure streaks; blank restores 600 seconds.",
			func(s *marshal.Settings) **int { return &s.InfraCooldownSeconds }),
		optIntField("verification-timeout-seconds", "seconds", "0", "next verification",
			"Zero leaves repository verification unlimited; blank uses the unlimited default.",
			func(s *marshal.Settings) **int { return &s.VerificationTimeoutSeconds }),
	)
	return rows
}

func editionEffective(s marshal.Settings, key string) (string, bool) {
	limits, cap := marshal.EffectiveWorkerLimits(s)
	if key == "worker-stall-minutes" {
		return strconv.Itoa(s.EffectiveWorkerStallMinutes()), true
	}
	if key == "global-cap" {
		return strconv.Itoa(cap), true
	}
	if strings.HasSuffix(key, "-limit") {
		if n, ok := limits[strings.TrimSuffix(key, "-limit")]; ok {
			return strconv.Itoa(n), true
		}
	}
	if key == "failover-order" {
		order := s.EffectiveEnabledFailoverOrder()
		if len(order) == 0 {
			return "none", true
		}
		return strings.Join(order, ","), true
	}
	if key == "infra-retry-limit" {
		return strconv.Itoa(s.EffectiveInfraRetryLimit()), true
	}
	if key == "infra-failover-streak" {
		return strconv.Itoa(s.EffectiveInfraFailoverStreak()), true
	}
	if key == "infra-cooldown-seconds" {
		return strconv.Itoa(s.EffectiveInfraCooldownSeconds()), true
	}
	if key == "verification-timeout-seconds" {
		return strconv.Itoa(s.EffectiveVerificationTimeoutSeconds()), true
	}
	return "", false
}

func configureEditionDescriptor(d *Descriptor) {
	switch d.Key {
	case "worker-stall-minutes":
		d.Help += " Zero or off disables the stall watchdog while keeping observation signals; blank restores 15 minutes."
	case "cross-review":
		d.Help = "Controls optional independent hand-in cross-review for upcoming runs. Omission is recorded per attempt; mandatory architecture verdicts remain required."
	case "design-review":
		d.Help = "Controls pre-approval independent design critique in a fresh session bound to the draft digest."
	case "architecture-conformance":
		d.Help = "Controls hand-in architecture conformance (enforce, warn, or off). Plans with a mandatory architecture obligation lock conformance to enforce."
	}
}
