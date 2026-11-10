package tui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/usage"
)

// limitBar is a three-cell bar of pct used: one cell per started third.
func limitBar(pct int, unicode bool) string {
	n := min(3, max(0, (pct*3+99)/100))
	if unicode {
		return strings.Repeat("▓", n) + strings.Repeat("░", 3-n)
	}
	return strings.Repeat("=", n) + strings.Repeat("-", 3-n)
}

// resetIn is how long until a reset, in its largest whole unit:
// "45m", "3h", "4d"; never "0h".
func resetIn(d time.Duration) string {
	if d <= 0 {
		return "now"
	}
	m := int(math.Ceil(float64(d) / float64(time.Minute)))
	if m < 60 {
		return fmt.Sprintf("%dm", m)
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", max(1, int(d.Hours())))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// limitFigure is a known quota's binding window as "42% ·4d", or "~42% ·4d"
// when the report is old; ok is false when nothing is known.
func limitFigure(q usage.Quota, now time.Time) (s string, pct int, ok bool) {
	win, found := q.Binding()
	if !found {
		return "", 0, false
	}
	pct = win.UsedShown()
	prefix := ""
	if q.Stale {
		prefix = "~"
	}
	return fmt.Sprintf("%s%d%% ·%s", prefix, pct, resetIn(win.ResetsAt.Sub(now))), pct, true
}

// limitReason is why a quota is unknown, in plain words. It never includes a
// path, a raw reason code or file content.
func limitReason(q usage.Quota) string {
	p := q.Provider
	switch q.Reason {
	case usage.NotExposed:
		return p + " does not write its usage limits where Marshal can read them"
	case usage.ReasonNoFile, usage.ReasonNotReported:
		return p + " has not reported its usage limits on this machine yet"
	case usage.ReasonStale:
		return p + "'s last usage report is more than a day old"
	case usage.ReasonReset:
		return "the limit reset since " + p + " last reported it"
	default:
		return p + "'s usage report could not be read"
	}
}
