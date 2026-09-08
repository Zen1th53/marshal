package harness

import (
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/model"
)

// This file derives the constitutional governance state of a harness and
// screens harness-native instructions.
//
// The governing idea is that MARSHAL must be able to say honestly how much it
// actually controls a tool. "We ran the command and nothing complained" is not
// governance. VERIFIED_GOVERNED is therefore reserved for the case where a
// current probe, backed by evidence, confirms the effective configuration; and
// the three honest alternatives are used whenever that is missing, rather than
// assuming control that has not been demonstrated (Article XIII).

// GovernanceAssessment is the evidence-derived judgement of how well MARSHAL
// governs one harness.
type GovernanceAssessment struct {
	Harness string                       `json:"harness"`
	State   constitution.GovernanceState `json:"state"`
	// Reasons explain the state in operator-facing terms.
	Reasons []string `json:"reasons,omitempty"`
	// ProbeEvidenceID is the evidence backing a verified assessment.
	ProbeEvidenceID string `json:"probe_evidence_id,omitempty"`
	// AssessedAt is when the judgement was made.
	AssessedAt time.Time `json:"assessed_at"`
}

// Governed reports whether MARSHAL has proven it controls the harness.
func (a GovernanceAssessment) Governed() bool { return a.State.Governed() }

// AssessGovernance derives the governance state of a harness profile.
//
// The order of checks matters: absence of the tool is reported as UNAVAILABLE
// rather than as a governance failure, an expired or unprobed profile is
// UNVERIFIED rather than assumed good, a version drift is DEGRADED because the
// probe describes a different build than the one installed, and only a
// current, evidence-backed profile with no unresolved capability questions
// reaches VERIFIED_GOVERNED.
func AssessGovernance(profile model.HarnessProfile, installedVersion string, now time.Time) GovernanceAssessment {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	assessment := GovernanceAssessment{
		Harness:         profile.Harness,
		ProbeEvidenceID: profile.ProbeEvidenceID,
		AssessedAt:      now,
	}

	// The tool is not present at all.
	if strings.TrimSpace(installedVersion) == "" {
		assessment.State = constitution.GovernanceUnavailable
		assessment.Reasons = []string{"The tool is not installed or could not be detected."}
		return assessment
	}
	if err := profile.Validate(); err != nil {
		assessment.State = constitution.GovernanceUnverified
		assessment.Reasons = []string{"No usable capability profile is recorded for this tool."}
		return assessment
	}

	var reasons []string

	// A probe that describes a different build than the one installed cannot
	// vouch for the installed one.
	if profile.InstalledVersion != installedVersion {
		reasons = append(reasons, "The recorded capability probe describes a different version than the one installed.")
	}
	// An expired probe is stale evidence, and stale evidence does not prove
	// current control.
	if !profile.ExpiresAt.IsZero() && !now.Before(profile.ExpiresAt) {
		reasons = append(reasons, "The capability probe has expired and must be repeated.")
	}
	// Governance claims rest on evidence, so a profile with none is unverified
	// however plausible its contents look.
	if strings.TrimSpace(profile.ProbeEvidenceID) == "" {
		assessment.State = constitution.GovernanceUnverified
		assessment.Reasons = append(reasons, "The capability profile is not backed by probe evidence.")
		return assessment
	}
	// Unresolved capability questions mean MARSHAL does not yet know what the
	// tool will do with a setting it is about to rely on.
	for feature, status := range profile.FeatureSupport {
		if status == model.StatusProbeRequired {
			reasons = append(reasons, "The behaviour of "+feature+" on this tool has not been established.")
		}
	}

	if len(reasons) > 0 {
		assessment.State = constitution.GovernanceDegraded
		assessment.Reasons = reasons
		return assessment
	}
	assessment.State = constitution.GovernanceVerified
	return assessment
}

// InstructionSource names where a native instruction was discovered.
type InstructionSource string

const (
	SourceHarnessConfig InstructionSource = "harness-config"
	SourceProjectFile   InstructionSource = "project-file"
	SourceEnvironment   InstructionSource = "environment"
	SourceProviderReply InstructionSource = "provider-reply"
)

// NativeInstruction is an instruction found in harness or project
// configuration. It is untrusted input: MARSHAL reads it to understand what a
// tool has been told, never to take orders from it.
type NativeInstruction struct {
	Source InstructionSource `json:"source"`
	Path   string            `json:"path,omitempty"`
	Text   string            `json:"text"`
}

// FirewallResult is the outcome of screening native instructions.
type FirewallResult struct {
	// Overrides names instructions that attempted to countermand a MARSHAL
	// rule. Their presence is a governance failure, not a preference.
	Overrides []string `json:"overrides,omitempty"`
	// Accepted are instructions that carry no authority claim and may inform
	// how MARSHAL configures the tool.
	Accepted []NativeInstruction `json:"accepted,omitempty"`
}

// Clean reports whether no override attempt was found.
func (r FirewallResult) Clean() bool { return len(r.Overrides) == 0 }

// overrideMarkers are phrases that signal an attempt to countermand MARSHAL.
//
// The list targets the meaning being claimed rather than any single phrasing,
// and the screen is deliberately one-way: a match is treated as an override
// attempt and reported, while a non-match grants nothing. Nothing a
// configuration file can say makes MARSHAL trust it more, so an attacker
// gains nothing by wording an instruction to avoid this list — the
// instruction still carries no authority either way. The value of the screen
// is that a deliberate attempt becomes visible rather than silent.
var overrideMarkers = []string{
	"bypass", "dangerously", "no-confirm", "skip approval", "skip_approval",
	"skip-approval", "disable sandbox", "disable_sandbox", "without sandbox",
	"ignore previous", "ignore all previous", "override policy", "override_policy",
	"disable policy", "no sandbox", "unrestricted network", "disable network",
	"auto approve", "auto_approve", "auto-approve", "always allow", "yolo",
	"grant all", "full access", "sudo", "root access", "system override",
	"constitution", "ignore marshal", "override marshal",
}

// ScreenInstructions classifies native instructions.
//
// Every instruction is untrusted regardless of the verdict. An accepted
// instruction may inform configuration; it never grants privilege, relaxes a
// gate, or outranks MARSHAL policy (invariant CI-015).
func ScreenInstructions(instructions []NativeInstruction) FirewallResult {
	var result FirewallResult
	for _, instruction := range instructions {
		lowered := strings.ToLower(instruction.Text)
		matched := ""
		for _, marker := range overrideMarkers {
			if strings.Contains(lowered, marker) {
				matched = marker
				break
			}
		}
		if matched != "" {
			location := string(instruction.Source)
			if instruction.Path != "" {
				location += " " + instruction.Path
			}
			// The marker is reported, never the surrounding text, so a
			// configuration file cannot smuggle content into an operator log
			// by embedding it next to a trigger word.
			result.Overrides = append(result.Overrides,
				location+" attempted to override MARSHAL policy ("+matched+")")
			continue
		}
		result.Accepted = append(result.Accepted, instruction)
	}
	return result
}
