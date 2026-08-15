package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"sort"
)

// PolicyDigest is the canonical textual SHA-256 binding of a normalized
// policy. It is a binding, not proof that the policy is currently active.
type PolicyDigest string

var policyDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func (d PolicyDigest) String() string { return string(d) }

func (d PolicyDigest) Validate() error {
	if !policyDigestPattern.MatchString(string(d)) {
		return NewError(CodeParseError, nil)
	}
	return nil
}

// Digest returns the deterministic digest of the validated normalized policy.
func (p Policy) Digest() (PolicyDigest, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}
	canonical := canonicalizePolicy(p)
	data, err := json.Marshal(canonical)
	if err != nil {
		return "", NewError(CodeParseError, err)
	}
	sum := sha256.Sum256(data)
	return PolicyDigest("sha256:" + hex.EncodeToString(sum[:])), nil
}

type canonicalPolicy struct {
	ID      PolicyID        `json:"id"`
	Version PolicyVersion   `json:"version"`
	Default Effect          `json:"default,omitempty"`
	Rules   []canonicalRule `json:"rules"`
}

type canonicalRule struct {
	ID          string            `json:"id"`
	Description string            `json:"description"`
	When        map[string]string `json:"when,omitempty"`
	Effect      Effect            `json:"effect"`
	Require     []Obligation      `json:"require,omitempty"`
	Priority    int               `json:"priority"`
}

func canonicalizePolicy(policy Policy) canonicalPolicy {
	rules := make([]Rule, len(policy.Rules))
	copy(rules, policy.Rules)
	sort.Slice(rules, func(i, j int) bool {
		if rules[i].ID != rules[j].ID {
			return rules[i].ID < rules[j].ID
		}
		return rules[i].Priority < rules[j].Priority
	})
	result := canonicalPolicy{ID: policy.ID, Version: policy.Version, Default: policy.Default, Rules: make([]canonicalRule, len(rules))}
	for i, rule := range rules {
		requirements := append([]Obligation(nil), rule.Require...)
		sort.Slice(requirements, func(i, j int) bool { return requirements[i] < requirements[j] })
		result.Rules[i] = canonicalRule{ID: rule.ID, Description: rule.Description, When: cloneAttributes(rule.When), Effect: rule.Effect, Require: requirements, Priority: rule.Priority}
	}
	return result
}
