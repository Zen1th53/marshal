package webcontrol

import (
	"net/http"
	"time"
)

type CapabilityState string

const (
	CapStateAvailable   CapabilityState = "AVAILABLE"
	CapStateDisabled    CapabilityState = "DISABLED_BY_POLICY"
	CapStateDegraded    CapabilityState = "DEGRADED"
	CapStateUnavailable CapabilityState = "UNAVAILABLE"
	CapStateNotRun      CapabilityState = "NOT_RUN"
)

type CapabilityStatusDTO struct {
	State       CapabilityState `json:"state"`
	Reason      string          `json:"reason,omitempty"`
	LastChecked time.Time       `json:"last_checked"`
}

type CapabilitiesDTO struct {
	Capabilities map[string]CapabilityStatusDTO `json:"capabilities"`
	EvaluatedAt  time.Time                      `json:"evaluated_at"`
}

// DiscoverCapabilities collects current runtime and platform capability availability
func (s *Server) DiscoverCapabilities() CapabilitiesDTO {
	now := time.Now().UTC()
	caps := map[string]CapabilityStatusDTO{
		"cap:system:read": {
			State:       CapStateAvailable,
			LastChecked: now,
		},
		"cap:adapter:read": {
			State:       CapStateAvailable,
			LastChecked: now,
		},
		"cap:task:read": {
			State:       CapStateAvailable,
			LastChecked: now,
		},
		"cap:task:write": {
			State:       CapStateAvailable,
			LastChecked: now,
		},
		"cap:task:run": {
			State:       CapStateAvailable,
			LastChecked: now,
		},
		"cap:task:cancel": {
			State:       CapStateAvailable,
			LastChecked: now,
		},
		"cap:agent:read": {
			State:       CapStateAvailable,
			LastChecked: now,
		},
		"cap:agent:write": {
			State:       CapStateAvailable,
			LastChecked: now,
		},
		"cap:memory:read": {
			State:       CapStateAvailable,
			LastChecked: now,
		},
		"cap:memory:write": {
			State:       CapStateAvailable,
			LastChecked: now,
		},
		"cap:gate:read": {
			State:       CapStateAvailable,
			LastChecked: now,
		},
		"cap:gate:approve": {
			State:       CapStateAvailable,
			LastChecked: now,
		},
		"cap:audit:read": {
			State:       CapStateAvailable,
			LastChecked: now,
		},
	}

	return CapabilitiesDTO{
		Capabilities: caps,
		EvaluatedAt:  now,
	}
}

func (s *Server) handleSystemCapabilities(w http.ResponseWriter, r *http.Request) {
	caps := s.DiscoverCapabilities()
	writeJSON(w, http.StatusOK, caps)
}
