package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/usage"
)

// TestPickerStaleZeroReadingDoesNotBlock: Claude's last reading, 16 hours old,
// said 0% left; its limit may have reset since without the CLI writing a new
// reading. The picker still lets Claude be chosen, saying the figure may be
// out of date, while a current 0% reading keeps blocking.
func TestPickerStaleZeroReadingDoesNotBlock(t *testing.T) {
	installPickerStubs(t, "claude")
	now := time.Now()
	quota := func(age time.Duration) usage.Quota {
		return usage.Quota{Provider: "claude", Known: true, AsOf: now.Add(-age), Stale: age >= usage.FreshFor,
			Windows: []usage.Window{{Label: "wk", LeftPercent: 0, ResetsAt: now.Add(24 * time.Hour)}}}
	}

	w := pickerWorkspace(t, quota(16*time.Hour))
	cands := w.marshalCandidates(t.Context())
	if len(cands) != 1 || cands[0].out || !cands[0].dated {
		t.Fatalf("old 0%% reading: candidates = %+v, want claude choosable and dated", cands)
	}
	th := NewTheme(ThemeNoColor, true, false)
	pv := boardVisual{theme: th, now: now}
	pv.pick = pickerView{cands: cands, sel: 0}
	if got := StripANSI(pv.pickerBody(120, 6, 0)[0]); !strings.Contains(got, "claude 0% left · read 16h ago, may be out of date") || strings.Contains(got, "out of limit") {
		t.Errorf("launch picker = %q, want the old reading marked as maybe out of date", got)
	}
	if w.decideMarshalPicker(t.Context()) {
		t.Fatal("an old 0% reading opened the picker instead of using the only provider")
	}
	if got := loadDefaultProvider(w.providerRoot()); got != "claude" {
		t.Fatalf("saved provider = %q, want claude", got)
	}

	fresh := pickerWorkspace(t, quota(5*time.Minute))
	if cands := fresh.marshalCandidates(t.Context()); len(cands) != 1 || !cands[0].out || cands[0].dated {
		t.Fatalf("current 0%% reading: candidates = %+v, want claude out of limit", cands)
	}
}
