//go:build !governed

package settingscatalog

import (
	"github.com/Zen1th53/marshal/internal/marshal"
	"strconv"
	"strings"
)

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
	d.set = func(s *marshal.Settings, raw string) error {
		raw = strings.NewReplacer("->", ",", " ", ",", "\t", ",").Replace(strings.ToLower(raw))
		parts := []string{}
		for _, p := range strings.Split(raw, ",") {
			if p != "" {
				parts = append(parts, p)
			}
		}
		s.FailoverOrder = strings.Join(parts, ",")
		return nil
	}
	return rows
}
func editionEffective(s marshal.Settings, key string) (string, bool) {
	limits, cap := marshal.EffectiveWorkerLimits(s)
	if key == "worker-stall-minutes" && s.WorkerStallMinutes == 0 {
		return "15", true
	}
	if key == "global-cap" {
		return strconv.Itoa(cap), true
	}
	if strings.HasSuffix(key, "-limit") {
		if n, ok := limits[strings.TrimSuffix(key, "-limit")]; ok {
			return strconv.Itoa(n), true
		}
	}
	if key == "failover-order" && s.FailoverOrder == "" {
		return strings.Join(marshal.DefaultProviderOrder(), ","), true
	}
	return "", false
}

func configureEditionDescriptor(d *Descriptor) {
	if d.Key == "worker-stall-minutes" {
		d.Help += " Zero uses the runtime default of 15 minutes."
	}
}
