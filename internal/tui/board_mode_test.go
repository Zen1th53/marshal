package tui

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/board/vm"
	"github.com/Zen1th53/marshal/internal/cloud"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/testutil/testcloud"
)

func TestBoardModeReportsEffectiveTierSeparately(t *testing.T) {
	w := NewWorkspace(nil, "project", "session")
	gate := testcloud.EntitledGate(t, testcloud.Options{Capabilities: []string{marshal.CapabilityMarshal, cloud.CapabilityDelegation}})
	w.AttachULTRA(gate, true)
	run := marshal.Run{State: marshal.Dispatching, Tier: marshal.Standard, Settings: marshal.DefaultSettings(), Process05Bound: true}
	w.state.Marshal = newMarshalPanel("run", "", run, "")
	var b vm.Board
	w.collectV2Mode(&b)
	m := b.Mode
	if m.EffectiveTier != string(marshal.Ultra) || m.RecordedRunTier != string(marshal.Standard) || m.EffectiveCap == nil || *m.EffectiveCap != 1 || m.PlanParallel == nil || *m.PlanParallel || *m.Active != 0 {
		t.Fatalf("serial plan: %+v", m)
	}
	if !strings.Contains(strings.Join(m.Reasons, ";"), "approved plan requires serial work") {
		t.Fatal(m.Reasons)
	}
	text := homeMode(m)
	for _, want := range []string{Edition(), "effective ultra", "recorded standard", "plan serial", "cap 1"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q: %s", want, text)
		}
	}
	v := boardVisual{theme: NewTheme(ThemeNoColor, true, false)}
	rendered := strings.Join(renderHome(v, b, homePrimary{}, 132, 24), "\n")
	for _, want := range []string{"Execution:", "effective ultra", "plan serial", "approved plan requires serial work"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("missing %q on home: %s", want, rendered)
		}
	}
	run.Process05Parallel = true
	run.Settings.UltraConcurrency = 3
	w.state.Marshal = newMarshalPanel("run", "", run, "")
	w.collectV2Mode(&b)
	expectedCap := 1
	if editionExtreme {
		expectedCap = 12
	}
	if *b.Mode.EffectiveCap != expectedCap {
		t.Fatalf("edition cap: %+v", b.Mode)
	}
	run.Tier, run.Process05Parallel = marshal.Ultra, true
	run.Tasks = []marshal.Task{{State: marshal.Dispatched}, {State: marshal.Dispatched}}
	w.state.Marshal = newMarshalPanel("run", "", run, "")
	w.AttachULTRA(gate, false)
	w.collectV2Mode(&b)
	m = b.Mode
	if m.EffectiveTier != string(marshal.Standard) || m.RecordedRunTier != string(marshal.Ultra) || *m.Active != 2 || *m.EffectiveCap != 1 || !*m.PlanParallel || !strings.Contains(strings.Join(m.Reasons, ";"), "draining") {
		t.Fatalf("drain: %+v", m)
	}
	w.AttachULTRA(cloud.NewGate("installation", "session", nil, nil), true)
	w.collectV2Mode(&b)
	if b.Mode.Entitlement != "inactive" || b.Mode.EffectiveTier != string(marshal.Standard) {
		t.Fatalf("unentitled: %+v", b.Mode)
	}
	w.state.Marshal = nil
	w.collectV2Mode(&b)
	if b.Mode.Active != nil || b.Mode.PlanParallel != nil || b.Mode.EffectiveCap != nil || b.Mode.RecordedRunTier != "" || b.Mode.RequestStatus != "unknown" {
		t.Fatalf("unobserved: %+v", b.Mode)
	}
}
