package harness

import (
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// Intelligence manages version-aware native capability profiles for agent harnesses.
type Intelligence struct{}

func NewIntelligence() *Intelligence {
	return &Intelligence{}
}

// DefaultProfiles returns the baseline feature profiles of the four
// first-class harnesses. They name no CLI version and no model: the
// installed version and the models it offers come only from probing the
// installed CLI (providers.Provider.Models), so a baseline never stands in
// for what is installed.
func (i *Intelligence) DefaultProfiles() map[string]model.HarnessProfile {
	now := time.Now().UTC()
	expiry := now.Add(24 * time.Hour)

	return map[string]model.HarnessProfile{
		"codex": {
			Harness:    "codex",
			BinaryPath: "codex",
			FeatureSupport: map[string]model.FeatureStatus{
				"instructions":      model.StatusNative,
				"headless":          model.StatusNative,
				"structured_output": model.StatusNative,
				"mcp_client":        model.StatusNative,
				"sandbox":           model.StatusNative,
				"session_resume":    model.StatusNative,
				"subagents":         model.StatusNative,
			},
			ReasoningKnobs:  []string{"reasoning_effort", "high", "medium", "low"},
			NativeModes:     []string{"non_interactive", "json_events"},
			ProbeEvidenceID: "ev-codex-probe-baseline",
			ProbedAt:        now,
			ExpiresAt:       expiry,
		},
		"claude-code": {
			Harness:    "claude-code",
			BinaryPath: "claude",
			FeatureSupport: map[string]model.FeatureStatus{
				"instructions":      model.StatusNative,
				"headless":          model.StatusNative,
				"structured_output": model.StatusNative,
				"mcp_client":        model.StatusNative,
				"sandbox":           model.StatusEmulated,
				"session_resume":    model.StatusNative,
				"hooks":             model.StatusNative,
				"subagents":         model.StatusNative,
			},
			ReasoningKnobs:  []string{"thinking_budget", "high", "standard"},
			NativeModes:     []string{"print", "structured_events"},
			ProbeEvidenceID: "ev-claude-probe-baseline",
			ProbedAt:        now,
			ExpiresAt:       expiry,
		},
		"opencode": {
			Harness:    "opencode",
			BinaryPath: "opencode",
			FeatureSupport: map[string]model.FeatureStatus{
				"instructions":      model.StatusNative,
				"headless":          model.StatusNative,
				"structured_output": model.StatusNative,
				"mcp_client":        model.StatusNative,
				"sandbox":           model.StatusEmulated,
				"session_resume":    model.StatusNative,
				"subagents":         model.StatusNative,
			},
			ReasoningKnobs:  []string{"code_mode_deep"},
			NativeModes:     []string{"code_mode", "headless_server"},
			ProbeEvidenceID: "ev-opencode-probe-baseline",
			ProbedAt:        now,
			ExpiresAt:       expiry,
		},
		"antigravity": {
			Harness:    "antigravity",
			BinaryPath: "agy",
			FeatureSupport: map[string]model.FeatureStatus{
				"instructions":      model.StatusNative,
				"headless":          model.StatusNative,
				"structured_output": model.StatusNative,
				"mcp_client":        model.StatusNative,
				"sandbox":           model.StatusNative,
				"session_resume":    model.StatusNative,
				"hooks":             model.StatusNative,
				"subagents":         model.StatusNative,
				"artifacts":         model.StatusNative,
			},
			ReasoningKnobs:  []string{"effort", "low", "medium", "high", "max"},
			NativeModes:     []string{"plan", "accept-edits", "print"},
			ProbeEvidenceID: "ev-antigravity-probe-baseline",
			ProbedAt:        now,
			ExpiresAt:       expiry,
		},
	}
}

// AuditKnob verifies if a requested configuration knob exists natively in the installed harness profile.
// Prevents inventing non-existent or deprecated flags from memory.
func (i *Intelligence) AuditKnob(profile model.HarnessProfile, requestedKnob string) model.FeatureStatus {
	normKnob := strings.TrimSpace(strings.ToLower(requestedKnob))

	// Security guardrail: bypass approvals is never permitted under MARSHAL authority
	if strings.Contains(normKnob, "bypass") || strings.Contains(normKnob, "dangerously") || strings.Contains(normKnob, "no-confirm") {
		return model.StatusUnsupported
	}

	if status, ok := profile.FeatureSupport[normKnob]; ok {
		return status
	}

	for _, knob := range profile.ReasoningKnobs {
		if strings.EqualFold(knob, normKnob) {
			return model.StatusNative
		}
	}

	for _, mode := range profile.NativeModes {
		if strings.EqualFold(mode, normKnob) {
			return model.StatusNative
		}
	}

	// Unknown or obsolete flag: must not be invented
	return model.StatusProbeRequired
}

// DetectDrift checks whether an installed version differs from the cached profile.
func (i *Intelligence) DetectDrift(cached model.HarnessProfile, currentInstalledVersion string) (bool, string) {
	if cached.InstalledVersion != currentInstalledVersion {
		return true, fmt.Sprintf("Version drift detected: cached %s vs installed %s; probe required",
			cached.InstalledVersion, currentInstalledVersion)
	}
	return false, ""
}
