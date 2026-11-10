// Package usage reads the usage quota a provider CLI has itself written to
// local files. It never estimates: a provider whose quota is not written
// locally, or whose last report is too old or has reset, is unknown and
// says why.
package usage

import (
	"math"
	"time"
)

// FreshFor is how long a report reads as current. Older reports are still
// shown, marked as old, until staleAfter.
const FreshFor = 30 * time.Minute

// Window is one rate-limit window. A known window has a ResetsAt after the
// time it was read; an unknown window has a zero ResetsAt and no percent.
type Window struct {
	Label       string
	LeftPercent float64
	ResetsAt    time.Time
}

// Known reports whether the window's percent can be shown.
func (w Window) Known() bool { return !w.ResetsAt.IsZero() }

// UsedShown is the percent used as shown: the percent left rounded down,
// so a figure never shows less used than the provider reported.
func (w Window) UsedShown() int { return min(100, max(0, 100-int(math.Floor(w.LeftPercent)))) }

// Quota is one provider's quota as last reported. Known is false when no
// window can be shown; Reason then says why.
type Quota struct {
	Provider string
	Windows  []Window
	Known    bool
	Reason   string
	// AsOf is when the provider wrote the figures; zero when unknown.
	AsOf time.Time
	// Stale is set on a Known quota whose figures are FreshFor old or more:
	// still shown, marked as old.
	Stale bool
}

// Binding is the known window closest to its limit: the highest used
// percent, and of equal ones the one that resets first. It returns
// (Window{}, false) when the quota is not known or no window is known.
func (q Quota) Binding() (Window, bool) {
	if !q.Known {
		return Window{}, false
	}
	var best Window
	found := false
	for _, w := range q.Windows {
		if !w.Known() {
			continue
		}
		if !found ||
			w.UsedShown() > best.UsedShown() ||
			(w.UsedShown() == best.UsedShown() && w.ResetsAt.Before(best.ResetsAt)) {
			best, found = w, true
		}
	}
	return best, found
}

// NotExposed is the reason for a provider that writes no quota locally.
const NotExposed = "not exposed"

// Providers is the order Read reports them in.
var Providers = []string{"codex", "claude", "opencode", "agy"}

// Sources is where each provider writes its quota; an empty field is unknown.
type Sources struct {
	CodexDir   string // Codex's sessions directory
	ClaudeFile string // Claude Code's .claude.json
}

// Read returns each provider's quota at now, in the order of Providers.
func Read(now time.Time, src Sources) []Quota {
	return []Quota{
		readCodex(now, src.CodexDir),
		readClaude(now, src.ClaudeFile),
		{Provider: "opencode", Reason: NotExposed},
		{Provider: "agy", Reason: NotExposed},
	}
}
