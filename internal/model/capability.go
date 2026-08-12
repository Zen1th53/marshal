package model

import "time"

type CapabilityState string

const (
	CapabilityUnknown     CapabilityState = "UNKNOWN"
	CapabilitySupported   CapabilityState = "SUPPORTED"
	CapabilityUnsupported CapabilityState = "UNSUPPORTED"
	CapabilityVerified   CapabilityState = "VERIFIED"
	CapabilityFailed      CapabilityState = "FAILED"
)

type EvidenceSource string

const (
	EvidenceStaticContract EvidenceSource = "STATIC_ADAPTER_CONTRACT"
	EvidenceBinaryProbe    EvidenceSource = "BINARY_PROBE"
	EvidenceAuthProbe      EvidenceSource = "AUTH_PROBE"
	EvidenceModelProbe     EvidenceSource = "MODEL_PROBE"
	EvidenceRealE2E        EvidenceSource = "REAL_E2E"
)

type ProviderCapability struct {
	Name        string          `json:"name"`
	State       CapabilityState `json:"state"`
	Source      EvidenceSource  `json:"source"`
	LastChecked time.Time       `json:"last_checked"`
	Detail      string          `json:"detail,omitempty"`
}

type ProviderStatus struct {
	Name          string               `json:"name"`
	Implemented   bool                 `json:"implemented"`
	Installed     bool                 `json:"installed"`
	BinaryPath    string               `json:"binary_path,omitempty"`
	Version       string               `json:"version,omitempty"`
	Authenticated CapabilityState      `json:"authenticated"`
	RealE2EStatus CapabilityState      `json:"real_e2e_status"`
	Capabilities  []ProviderCapability `json:"capabilities"`
}
