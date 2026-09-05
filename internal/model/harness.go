package model

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrHarnessInvalid = errors.New("invalid harness profile")
)

type FeatureStatus string

const (
	StatusNative        FeatureStatus = "native"
	StatusEmulated      FeatureStatus = "emulated"
	StatusProbeRequired FeatureStatus = "probe_required"
	StatusUnsupported   FeatureStatus = "unsupported"
)

func (s FeatureStatus) IsValid() bool {
	switch s {
	case StatusNative, StatusEmulated, StatusProbeRequired, StatusUnsupported:
		return true
	default:
		return false
	}
}

// HarnessProfile records the probed, version-aware capability intelligence for a native agent CLI/runtime.
type HarnessProfile struct {
	Harness          string                   `json:"harness"`
	InstalledVersion string                   `json:"installed_version"`
	BinaryPath       string                   `json:"binary_path"`
	SupportedModels  []string                 `json:"supported_models"`
	DefaultModel     string                   `json:"default_model"`
	FeatureSupport   map[string]FeatureStatus `json:"feature_support"`
	ReasoningKnobs   []string                 `json:"reasoning_knobs,omitempty"`
	NativeModes      []string                 `json:"native_modes,omitempty"`
	ProbeEvidenceID  string                   `json:"probe_evidence_id,omitempty"`
	ProbedAt         time.Time                `json:"probed_at"`
	ExpiresAt        time.Time                `json:"expires_at"`
}

func (p HarnessProfile) Validate() error {
	if strings.TrimSpace(p.Harness) == "" {
		return fmt.Errorf("%w: harness name is required", ErrHarnessInvalid)
	}
	if strings.TrimSpace(p.InstalledVersion) == "" {
		return fmt.Errorf("%w: installed version is required", ErrHarnessInvalid)
	}
	if p.ProbedAt.IsZero() {
		return fmt.Errorf("%w: probed_at timestamp is required", ErrHarnessInvalid)
	}
	return nil
}

func (p HarnessProfile) IsFresh(now time.Time) bool {
	if p.ExpiresAt.IsZero() {
		return true
	}
	return now.Before(p.ExpiresAt)
}

// ULTRARouteRequest specifies the multidimensional requirements for intelligent routing.
type ULTRARouteRequest struct {
	GoalID               string   `json:"goal_id"`
	GoalRevision         int64    `json:"goal_revision"`
	TaskID               string   `json:"task_id"`
	FixedRole            Role     `json:"fixed_role"`
	Risk                 Risk     `json:"risk"`
	HasCriticalClaims    bool     `json:"has_critical_claims"`
	Scope                []string `json:"scope"`
	PreferredHarness     string   `json:"preferred_harness,omitempty"`
	RequiredCapabilities []string `json:"required_capabilities,omitempty"`
	MultipleDecoupled    bool     `json:"multiple_decoupled,omitempty"`
}

// ULTRARoutePlan defines the end-to-end operational configuration selected for the turn.
type ULTRARoutePlan struct {
	TaskID             string            `json:"task_id"`
	Role               Role              `json:"role"`
	Harness            string            `json:"harness"`
	Model              string            `json:"model"`
	NativeMode         string            `json:"native_mode"`
	ReasoningEffort    string            `json:"reasoning_effort"` // "high", "medium", "low", "none"
	UseSubagents       bool              `json:"use_subagents"`
	ToolPolicy         string            `json:"tool_policy"`
	ContextStrategy    string            `json:"context_strategy"`
	VerificationPolicy string            `json:"verification_policy"`
	Explanation        string            `json:"explanation"`
	SelectedKnobs      map[string]string `json:"selected_knobs,omitempty"`
}
