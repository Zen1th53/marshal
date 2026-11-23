package app

import (
	"context"

	"github.com/Zen1th53/marshal/internal/cloud"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/plan"
)

type admissionGateKey struct{}

type admittedExecutionGate struct {
	marshal.CapabilityGate
	enabled bool
}

func (g admittedExecutionGate) Capability(name string) bool {
	return g.CapabilityGate != nil && g.CapabilityGate.Capability(name)
}
func (g admittedExecutionGate) ExecutionEnabled() bool { return g.enabled }

// reserveExecution freezes the execution preference under the owner's admission
// fence. Capabilities remain live so a reservation never extends entitlement.
func reserveExecution(gate marshal.CapabilityGate) (marshal.CapabilityGate, func()) {
	release := func() {}
	if owner, ok := gate.(interface{ LockAdmission() func() }); ok {
		release = owner.LockAdmission()
	}
	enabled := true
	if g, ok := gate.(interface{ ExecutionEnabled() bool }); ok {
		enabled = g.ExecutionEnabled()
	}
	return admittedExecutionGate{CapabilityGate: gate, enabled: enabled}, release
}

func (s *MarshalService) admissionGate(ctx context.Context) marshal.CapabilityGate {
	if gate, ok := ctx.Value(admissionGateKey{}).(marshal.CapabilityGate); ok {
		return gate
	}
	return s.Gate
}

// projectExecutionGate also reads preferences for project-owned native services.
// Read failures refuse parallel admission until the preference can be read again.
type projectExecutionGate struct {
	marshal.CapabilityGate
	service *MarshalService
}

func (g projectExecutionGate) Capability(name string) bool {
	return g.CapabilityGate != nil && g.CapabilityGate.Capability(name)
}
func (g projectExecutionGate) ExecutionEnabled() bool {
	if g.service == nil || g.service.Store == nil || g.service.ProjectID == "" {
		return false
	}
	preference, err := g.service.Store.GetUltraExecutionPreference(context.Background(), g.service.ProjectID)
	if err != nil {
		return false
	}
	if preference != nil {
		return *preference
	}
	return cloud.ExecutionOnAtStartup
}

// EffectiveGate returns the live capability gate, reading the persisted
// project execution preference when the attached gate does not expose one.
func (s *MarshalService) EffectiveGate() marshal.CapabilityGate {
	if s == nil || s.Gate == nil {
		return nil
	}
	if _, live := s.Gate.(interface{ ExecutionEnabled() bool }); live {
		return s.Gate
	}
	if s.Store != nil && s.ProjectID != "" {
		return projectExecutionGate{CapabilityGate: s.Gate, service: s}
	}
	return s.Gate
}

// EffectiveDraftPlanMode selects the plan mode for a new draft from the live
// tier policy. Only eligible default-edition drafts receive plan.ModeUltra;
// governed-edition Process 05 remains serial.
func EffectiveDraftPlanMode(gate marshal.CapabilityGate, settings marshal.Settings) plan.Mode {
	if isExtremeBuild && marshal.TierPolicy(gate, settings).Tier == marshal.Ultra {
		return plan.ModeUltra
	}
	return plan.ModeStandard
}
