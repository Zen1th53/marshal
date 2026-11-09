// Package usage reads the usage quota a provider CLI has itself written to
// local files. It never estimates: a provider or window whose quota is not
// exposed, or whose last report can no longer be trusted, is unknown and
// says why.
package usage

import "time"

// Window is one rate-limit window. A known window has a ResetsAt after the
// time it was read; an unknown window has a zero ResetsAt and no percent.
type Window struct {
	Label       string
	LeftPercent float64
	ResetsAt    time.Time
}

// Known reports whether the window's percent can be shown.
func (w Window) Known() bool { return !w.ResetsAt.IsZero() }

// Quota is one provider's quota as last reported. Known is false when no
// window can be shown; Reason then says why.
type Quota struct {
	Provider string
	Windows  []Window
	Known    bool
	Reason   string
}

// NotExposed is the reason for a provider that writes no quota locally.
const NotExposed = "not exposed"

// Providers is the order Read reports them in.
var Providers = []string{"codex", "claude", "opencode", "agy"}

// Read returns each provider's quota at now, in the order of Providers.
// codexDir is Codex's sessions directory; empty means it is unknown.
func Read(now time.Time, codexDir string) []Quota {
	out := []Quota{readCodex(now, codexDir)}
	for _, p := range Providers[1:] {
		out = append(out, Quota{Provider: p, Reason: NotExposed})
	}
	return out
}
