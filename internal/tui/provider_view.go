package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
)

// providerPane is the Providers view's one pane: the Board's body, between
// its header and footer.
const providerPane layout.PaneID = "providers"

// providerSnapshot is the Providers view's data: the last overview read
// and when it was read.
type providerSnapshot struct {
	Providers []store.ProviderStatus
	// Loaded is set once a read has succeeded; At is when the last read
	// finished and Err why it failed, if it did.
	Loaded bool
	At     time.Time
	Err    string
}

// providerPaint is what one frame of the Providers view needs.
type providerPaint struct {
	Snap providerSnapshot
	// Selected is the provider the verify key acts on.
	Selected string
	// Busy is the provider whose harness is being verified; Routing the
	// one whose tasks are being routed around it.
	Busy, Routing string
	// Route is the route around Selected waiting for Enter, if any.
	Route providerRoute
	// Verify is the provider whose harness probe waits for Enter.
	Verify string
}

// providerState is a provider's routing state: "limit" while a cooldown
// runs, "re-verify" when its harness profile is missing or expired, else
// "ready".
func providerState(p store.ProviderStatus, now time.Time) string {
	switch {
	case providerInLimit(p, now):
		return "limit"
	case p.Expired || p.VerifiedAt.IsZero():
		return "re-verify"
	}
	return "ready"
}

// providerInLimit is whether the provider's newest cooldown still runs.
func providerInLimit(p store.ProviderStatus, now time.Time) bool {
	return !p.LimitUntil.IsZero() && p.LimitUntil.After(now)
}

func providerUntil(t, now time.Time) string {
	return t.In(now.Location()).Format("Jan 2 15:04")
}

var providerStateColour = map[string]string{"ready": "merged", "limit": "failed", "re-verify": "waiting"}

// providerRender paints the Providers view into a w×h body, keeping the
// selected provider in view.
func (v boardVisual) providerRender(m providerPaint, w, h int) []string {
	all, selAt := v.providerLines(m, w)
	scroll := 0
	if selAt >= 0 && selAt >= h-1 {
		scroll = selAt - h + 2
	}
	scroll = min(max(0, scroll), max(0, len(all)-h))
	out := slices.Clone(all[scroll:min(len(all), scroll+h)])
	for i := range out {
		out[i] = PadCell(out[i], w)
	}
	if !v.unicode() {
		out = hostASCII(out, w)
	}
	return out
}

// providerCols are the table's column widths; limit takes what is left.
type providerCols struct{ prov, model, verified, lanes, limit, resets, state int }

func providerColumns(w int) providerCols {
	c := providerCols{prov: 10, model: 22, verified: 12, lanes: 5, resets: 14, state: 10}
	if w < 110 {
		c = providerCols{prov: 9, model: 14, verified: 10, lanes: 5, resets: 13, state: 9}
	}
	fixed := 2 + c.prov + c.model + c.verified + c.lanes + c.resets + c.state + 7
	c.limit = min(30, max(8, w-fixed))
	return c
}

// providerLines lays the view out as the prototype does: the PROVIDERS
// title, one row per provider, the selected provider's keys and a note.
// It also returns the line the selected provider is on, or -1.
func (v boardVisual) providerLines(m providerPaint, w int) ([]string, int) {
	s := m.Snap
	summary := "harness profiles expire after 24 h · Marshal routes new lanes only to ready providers"
	if VisibleLen("PROVIDERS "+summary) > w {
		summary = "profiles expire after 24 h · new lanes go to ready providers"
	}
	out := []string{v.title("PROVIDERS", summary, false, w)}
	if !s.Loaded {
		read := "reading the store…"
		if s.Err != "" {
			read = v.fg("failed", "not readable: "+cleanBoard(s.Err))
		}
		return append(out, v.kv("updated", read, w)), -1
	}
	read := "read " + hostAgo(v.now.Sub(s.At)) + " · every 10 s while open"
	if s.Err != "" {
		read = v.fg("failed", "last read failed: "+cleanBoard(s.Err)) + v.fg("dim", " · showing "+hostAgo(v.now.Sub(s.At)))
	}
	out = append(out, v.kv("updated", read, w), "")
	c := providerColumns(w)
	cell := func(s string, n int) string { return PadCell(s, n) }
	row := func(dot, prov, model, verified, lanes, limit, resets, state string) string {
		return strings.Join([]string{cell(dot, 1), cell(prov, c.prov), cell(model, c.model), cell(verified, c.verified), fmt.Sprintf("%*s", c.lanes, lanes), cell(limit, c.limit), cell(resets, c.resets), cell(state, c.state)}, " ")
	}
	out = append(out, v.fg("dim", PadCell(row("", "provider", "model", "verified", "lanes", "limit", "resets", "state"), w)))
	if len(s.Providers) == 0 {
		return append(out, v.fg("dim", PadCell("  no harness profiles and no lanes yet · /harness verify <provider> <model>", w))), -1
	}
	sel := -1
	var selected *store.ProviderStatus
	for i, p := range s.Providers {
		st := providerState(p, v.now)
		col := providerStateColour[st]
		verified := v.fg("dim", "never")
		if !p.VerifiedAt.IsZero() {
			ago := hostAgo(v.now.Sub(p.VerifiedAt))
			if p.Expired {
				verified = v.fg("waiting", ago)
			} else {
				verified = v.fg("dim", ago)
			}
		}
		limit, resets := v.fg("dim", "—"), v.fg("dim", "—")
		switch {
		case st == "limit":
			limit = v.fg("failed", cleanBoard(p.LastLimitReason))
			resets = providerUntil(p.LimitUntil, v.now)
		case !p.LastLimitAt.IsZero():
			limit = v.fg("dim", cleanBoard(p.LastLimitReason))
			resets = v.fg("dim", "was "+hostAgo(v.now.Sub(p.LastLimitAt)))
		}
		state := st
		if m.Busy == p.Provider {
			state, col = "verifying", "working"
		}
		line := row(v.fg(col, "●"), cleanBoard(p.Provider), v.fg("dim", firstNonEmpty(cleanBoard(p.Model), "—")), verified, fmt.Sprint(p.ActiveLanes), limit, resets, v.fg(col, state))
		line = PadCell(line, w)
		if p.Provider == m.Selected {
			sel = len(out)
			selected = &s.Providers[i]
			line = v.bg("sel", line)
		}
		out = append(out, line)
	}
	if selected == nil {
		return out, sel
	}
	p := *selected
	keys := v.bg("rule", "[v]") + " verify harness  " + v.bg("rule", "[r]") + " route around"
	note := ""
	switch providerState(p, v.now) {
	case "limit":
		keys = v.fg("dim", "[v] verify harness · waits for the limit to end") + "  " + v.bg("rule", "[r]") + " route around"
		note = fmt.Sprintf("running %s lanes finish; new work waits for %s or a ready one.", cleanBoard(p.Provider), providerUntil(p.LimitUntil, v.now))
	case "re-verify":
		if p.VerifiedAt.IsZero() {
			note = fmt.Sprintf("%s has no harness profile; Marshal routes no new lanes to it until [v] verifies it.", cleanBoard(p.Provider))
		} else {
			note = fmt.Sprintf("%s's profile expired; [v] verifies %s again before new lanes go to it.", cleanBoard(p.Provider), firstNonEmpty(cleanBoard(p.Model), "its default model"))
		}
	default:
		note = fmt.Sprintf("%s is ready; its profile holds until %s.", cleanBoard(p.Provider), providerUntil(p.ExpiresAt, v.now))
		if p.ExpiresAt.IsZero() {
			note = cleanBoard(p.Provider) + " is ready."
		}
	}
	switch {
	case m.Busy != "":
		keys = v.fg("waiting", "verifying "+cleanBoard(m.Busy)+"…")
	case m.Routing != "":
		keys = v.fg("waiting", "routing around "+cleanBoard(m.Routing)+"…")
	case m.Verify == p.Provider:
		keys = v.fg("waiting", fmt.Sprintf("Enter to verify %s's harness (calls the model) · Esc cancels", cleanBoard(p.Provider)))
	case m.Route.From == p.Provider:
		keys = v.fg("waiting", fmt.Sprintf("Enter to route %s to %s · Esc cancels", cleanBoard(strings.Join(m.Route.Tasks, ", ")), cleanBoard(m.Route.To)))
	}
	out = append(out, "", PadCell("  "+keys, w), v.fg("dim", PadCell("  "+note, w)))
	return out, sel
}
