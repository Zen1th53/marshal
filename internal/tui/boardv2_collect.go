package tui

import (
	"context"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/cloud"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/tui/board/vm"
)

// boardV2 builds one frame of the Board v2 view model from the workspace.
// Each collector owns its own fields; none reads another's output.
func (w *Workspace) boardV2() vm.Board {
	var b vm.Board
	w.collectV2Flow(&b)
	w.collectV2Marshal(&b)
	w.collectV2Chat(&b)
	w.collectV2Cards(&b)
	w.collectV2System(&b)
	w.collectV2MarshalSays(&b)
	w.collectV2Mode(&b)
	applyReadiness(&b, w.executionReadiness())
	b.Web = w.webState()
	return b
}

func (w *Workspace) collectV2Mode(b *vm.Board) {
	state := w.GetUIState()
	gate, enabled := w.ultraGate()
	m := vm.Mode{Edition: Edition(), Entitlement: "inactive", RequestStatus: "unknown", ExecutionEnabled: &enabled}
	if gate.Entitled() {
		m.Entitlement = "active"
	}
	delegation := gate.Capability(cloud.CapabilityDelegation)
	m.Delegation = &delegation
	// The frame is built often; a slow request store must not stall it.
	readCtx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	observation, found, err := w.readUltraRequest(readCtx)
	cancel()
	if err == nil && found {
		m.RequestStatus = requestPresentation(observation, found).State
	}
	run := marshal.Run{Settings: marshal.DefaultSettings()}
	if p := state.Marshal; p != nil && p.run.State != "" {
		run = p.run
		m.RecordedRunTier = string(run.Tier)
		if run.Process05Bound {
			parallel := run.Process05Parallel
			m.PlanParallel = &parallel
		}
		active := 0
		for _, task := range run.Tasks {
			if task.State == marshal.Dispatched {
				active++
			}
		}
		m.Active = &active
	}
	policy, cap, reasons := app.ExecutionCapacity(modeGate{gate, enabled}, run)
	m.EffectiveTier = string(policy.Tier)
	// A cap needs the observed run settings, rather than assumed defaults.
	if m.Active != nil {
		m.EffectiveCap = &cap
	}
	if w.ultraPreferenceReadErr() != nil {
		// An unreadable preference is not the owner's choice to turn it off.
		for i, r := range reasons {
			if r == "ULTRA execution preference is off" {
				reasons[i] = "ULTRA execution preference could not be read; running serially"
			}
		}
	}
	m.Reasons = reasons
	b.Mode = m
	b.Ready.SessionLabel = state.SessionMode
}

type modeGate struct {
	marshal.CapabilityGate
	enabled bool
}

func (g modeGate) ExecutionEnabled() bool { return g.enabled }
