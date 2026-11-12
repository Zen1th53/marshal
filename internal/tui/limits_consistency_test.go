package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/usage"
)

// TestLimitsSameQuantityEverywhere: one provider reading (codex, 35% left
// in its weekly window, as Codex itself reports it) reads the same way in
// the launch picker, the vitals row, the chat title and the Providers view:
// as "35% left", never as the 65% used, and the bar shows what is left.
func TestLimitsSameQuantityEverywhere(t *testing.T) {
	now := vitalsNow
	codex := usage.Quota{Provider: "codex", Known: true, AsOf: now.Add(-time.Minute),
		Windows: []usage.Window{{Label: "wk", LeftPercent: 35, ResetsAt: now.Add(2*24*time.Hour + time.Hour)}}}
	th := NewTheme(ThemeNoColor, true, false)

	left, known, _ := providerLeft(codex)
	pv := boardVisual{theme: th, now: now}
	pv.pick = pickerView{cands: []pickerCand{{provider: "codex", left: left, known: known}}, sel: 0}
	picker := StripANSI(pv.pickerBody(120, 6, 0)[0])

	vv := vitalsFixture(th)
	vv.vitals.quotas = []usage.Quota{codex}
	vv.vitals.installed = map[string]bool{"codex": true}
	vitals, _ := vv.vitalsLine(160)
	vitals = StripANSI(vitals)

	w := &Workspace{}
	w.storeUsage([]usage.Quota{codex})
	title := w.dockTitle(&activeTmuxAgent{provider: "codex"}, 0)

	m := demoProviderPaint(now)
	// codex is at its limit in the demo, so the Providers row reads claude's.
	claude := codex
	claude.Provider = "claude"
	m.Quotas = map[string]usage.Quota{"claude": claude}
	pl, _ := boardVisual{theme: th, now: now}.providerLines(m, 140)
	providers := strings.Join(strings.Fields(StripANSI(strings.Join(pl, "\n"))), " ")

	for name, got := range map[string]string{"picker": picker, "vitals": vitals, "title": title} {
		if !strings.Contains(got, "codex") || !strings.Contains(got, "35% left") || strings.Contains(got, "65%") {
			t.Errorf("%s shows %q, want codex at 35%% left and no 65%%", name, got)
		}
	}
	for name, got := range map[string]string{"vitals": vitals, "title": title} {
		if !strings.Contains(got, "▓░░ 35% left") {
			t.Errorf("%s bar does not show what is left: %q", name, got)
		}
	}
	if !strings.Contains(providers, "▓░░ 35% left · wk") || strings.Contains(providers, "65%") {
		t.Errorf("Providers view shows the used percent: %s", providers)
	}
}

// TestLimitsUnknownWordAndGlyphs: an unknown limit reads "limit unknown"
// wherever it is shown (never "unavailable", which reads as "cannot be
// used"), and each provider has one glyph, the picker's included.
func TestLimitsUnknownWordAndGlyphs(t *testing.T) {
	th := NewTheme(ThemeNoColor, true, false)
	pv := boardVisual{theme: th, now: vitalsNow}
	pv.pick = pickerView{cands: []pickerCand{{provider: "claude"}, {provider: "opencode"}}, sel: 0}
	picker := StripANSI(pv.pickerBody(120, 6, 0)[0])

	vv := vitalsFixture(th)
	vv.vitals.quotas = []usage.Quota{{Provider: "claude", Reason: usage.ReasonNoFile}}
	vv.vitals.installed = map[string]bool{"claude": true}
	vitals, _ := vv.vitalsLine(160)
	vitals = StripANSI(vitals)

	w := &Workspace{}
	w.storeUsage([]usage.Quota{{Provider: "claude", Reason: usage.ReasonNoFile}})
	title := w.dockTitle(&activeTmuxAgent{provider: "claude"}, 0)

	for name, got := range map[string]string{"picker": picker, "vitals": vitals, "title": title} {
		if !strings.Contains(got, "claude limit unknown") || strings.Contains(got, "unavailable") {
			t.Errorf("%s shows %q, want claude limit unknown", name, got)
		}
	}
	for _, p := range []string{"codex", "claude", "agy", "opencode"} {
		if g := pv.pickerGlyph(p); g != providerGlyph(p, true) {
			t.Errorf("picker glyph for %s = %q, elsewhere %q", p, g, providerGlyph(p, true))
		}
	}
	if !strings.Contains(picker, providerGlyph("opencode", true)+" opencode") {
		t.Errorf("picker = %q, want opencode with its one glyph", picker)
	}
}
