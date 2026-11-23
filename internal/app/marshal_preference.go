package app

import (
	"context"

	"github.com/Zen1th53/marshal/internal/cloud"
	"github.com/Zen1th53/marshal/internal/marshal"
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
	preference, err := g.service.Store.GetUltraExecutionPreference(context.Background(), g.service.ProjectID)
	if err != nil {
		return false
	}
	if preference != nil {
		return *preference
	}
	return cloud.ExecutionOnAtStartup
}
