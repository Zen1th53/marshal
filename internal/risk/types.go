package risk

import (
	"strings"
	"unicode/utf8"
)

type AssessmentID string
type ActionDigest string
type PolicyDigest string

type AssessmentState string

const (
	StateRequested           AssessmentState = "requested"
	StateClassified          AssessmentState = "classified"
	StateRequirementsEmitted AssessmentState = "requirements_emitted"
)

func ValidateState(state AssessmentState) error {
	switch state {
	case StateRequested, StateClassified, StateRequirementsEmitted:
		return nil
	default:
		return ErrDescriptorInvalid
	}
}

// Level is ordered from least to most dangerous. The order is part of the
// contract; it is not an authorization decision.
type Level string

const (
	LevelLow      Level = "low"
	LevelMedium   Level = "medium"
	LevelHigh     Level = "high"
	LevelCritical Level = "critical"
)

func (l Level) Valid() bool {
	switch l {
	case LevelLow, LevelMedium, LevelHigh, LevelCritical:
		return true
	default:
		return false
	}
}

func (l Level) Rank() int {
	switch l {
	case LevelLow:
		return 1
	case LevelMedium:
		return 2
	case LevelHigh:
		return 3
	case LevelCritical:
		return 4
	default:
		return 0
	}
}

// Factors are structured tool metadata. Raw command parsing is deliberately
// not represented here; adapters must provide these facts explicitly.
type Factors struct {
	Destructive         bool `json:"destructive"`
	ExternalWrite       bool `json:"external_write"`
	SecretUse           bool `json:"secret_use"`
	PrivilegeEscalation bool `json:"privilege_escalation"`
	Network             bool `json:"network"`
	Deploy              bool `json:"deploy"`
	ScopeBreadth        int  `json:"scope_breadth"`
}

func (f Factors) Validate() error {
	if f.ScopeBreadth < 0 {
		return ErrDescriptorInvalid
	}
	return nil
}

// ToolDescriptor is the normalized, provider-neutral description assessed by
// the risk engine.
type ToolDescriptor struct {
	Tool         string  `json:"tool"`
	Action       string  `json:"action"`
	Resource     string  `json:"resource"`
	Factors      Factors `json:"factors"`
	ClaimedLevel Level   `json:"claimed_level,omitempty"`
}

func (d ToolDescriptor) Validate() error {
	for _, value := range []string{d.Tool, d.Action, d.Resource} {
		if !safeText(value) {
			return ErrDescriptorInvalid
		}
	}
	if d.ClaimedLevel != "" && !d.ClaimedLevel.Valid() {
		return ErrDescriptorInvalid
	}
	return d.Factors.Validate()
}

// Assessment is an immutable result of classifying one action descriptor.
// Risk is informative and requirement-producing; it never authorizes an
// operation by itself.
type Assessment struct {
	ID                   AssessmentID    `json:"assessment_id"`
	ActionDigest         ActionDigest    `json:"action_digest"`
	Level                Level           `json:"level"`
	Score                int             `json:"score"`
	Factors              Factors         `json:"factors"`
	RequiredAuthorities  []string        `json:"required_authorities,omitempty"`
	RequiredCapabilities []string        `json:"required_capabilities,omitempty"`
	PolicyDigest         PolicyDigest    `json:"policy_digest,omitempty"`
	State                AssessmentState `json:"state"`
}

func (a Assessment) Validate() error {
	if !safeText(string(a.ID)) || !a.Level.Valid() || a.Score < 0 {
		return ErrDescriptorInvalid
	}
	if err := ValidateState(a.State); err != nil {
		return err
	}
	if err := a.Factors.Validate(); err != nil {
		return err
	}
	if a.ActionDigest != "" && !safeText(string(a.ActionDigest)) {
		return ErrDescriptorInvalid
	}
	if a.PolicyDigest != "" && !safeText(string(a.PolicyDigest)) {
		return ErrDescriptorInvalid
	}
	if !validRequirements(a.RequiredAuthorities) || !validRequirements(a.RequiredCapabilities) {
		return ErrDescriptorInvalid
	}
	return nil
}

func validRequirements(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !safeText(value) {
			return false
		}
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func safeText(value string) bool {
	if strings.TrimSpace(value) == "" || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}
