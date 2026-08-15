package policy

import (
	"context"
	"sort"
	"strings"
)

// Effect is the closed outcome vocabulary for a policy rule.
type Effect string

const (
	EffectAllow   Effect = "allow"
	EffectDeny    Effect = "deny"
	EffectRequire Effect = "require"
)

func (e Effect) Valid() bool {
	switch e {
	case EffectAllow, EffectDeny, EffectRequire:
		return true
	default:
		return false
	}
}

// PolicyID, PolicyVersion, Action and Resource keep policy identity fields
// distinct at API boundaries while remaining straightforward YAML/JSON values.
type PolicyID string
type PolicyVersion int
type Action string
type Resource string

// Obligation is a bounded requirement returned by a require rule. Obligations
// never grant authority by themselves.
type Obligation string

const (
	ObligationApproval     Obligation = "REQUIRE_APPROVAL"
	ObligationVerification Obligation = "REQUIRE_VERIFICATION"
	ObligationNetworkDeny  Obligation = "NETWORK_DENY"
	ObligationReadOnly     Obligation = "READ_ONLY"
	ObligationPathScope    Obligation = "PATH_SCOPE"
)

func (o Obligation) Valid() bool {
	switch o {
	case ObligationApproval, ObligationVerification, ObligationNetworkDeny, ObligationReadOnly, ObligationPathScope:
		return true
	default:
		return false
	}
}

// Rule is one deterministic policy rule. When contains facts that must match
// the evaluation request's bounded attributes exactly.
type Rule struct {
	ID          string            `yaml:"id" json:"id"`
	Description string            `yaml:"description" json:"description"`
	When        map[string]string `yaml:"when" json:"when"`
	Effect      Effect            `yaml:"effect" json:"effect"`
	Require     []Obligation      `yaml:"require" json:"require"`
	Priority    int               `yaml:"priority" json:"priority"`
}

// Policy is the strict, implementation-neutral policy contract. Persistence
// and activation are owned by later T48 units; this type is an in-memory
// validated value only.
type Policy struct {
	ID      PolicyID      `yaml:"id" json:"id"`
	Version PolicyVersion `yaml:"version" json:"version"`
	Default Effect        `yaml:"default" json:"default"`
	Rules   []Rule        `yaml:"rules" json:"rules"`
}

// PolicyBinding is the freshness value a policy owner compares against its
// current canonical binding. A valid digest alone is not freshness proof.
type PolicyBinding struct {
	Version    PolicyVersion
	Digest     PolicyDigest
	Generation uint64
}

func (b PolicyBinding) Validate() error {
	if b.Version <= 0 {
		return NewError(CodeParseError, nil)
	}
	return b.Digest.Validate()
}

// FreshAgainst is intentionally exact: any version, digest, or generation
// change invalidates a previously issued decision.
func (b PolicyBinding) FreshAgainst(current PolicyBinding) bool {
	return b.Validate() == nil && current.Validate() == nil && b == current
}

// EvaluationRequest contains facts supplied by an authenticated caller. The
// evaluator treats provider and attributes as data, never as authority.
type EvaluationRequest struct {
	SubjectID  string
	TaskID     string
	ChangeID   string
	Action     Action
	Resource   Resource
	Provider   string
	Attributes map[string]string
}

// Decision is immutable from the evaluator's perspective. It is denied by
// default; requirements do not imply Allowed.
type Decision struct {
	Allowed      bool
	DeniedBy     string
	AllowedBy    string
	Requirements []Obligation
	PolicyDigest PolicyDigest
	Binding      PolicyBinding
	Effect       Effect
}

// Validate rejects decisions that could accidentally turn an incomplete or
// unknown effect into authority.
func (d Decision) Validate() error {
	if err := d.PolicyDigest.Validate(); err != nil {
		return err
	}
	if !d.Effect.Valid() || (d.Allowed && d.Effect != EffectAllow) || (!d.Allowed && d.Effect == EffectAllow) {
		return NewError(CodeInvalidDecision, nil)
	}
	if d.DeniedBy != "" && d.AllowedBy != "" {
		return NewError(CodeInvalidDecision, nil)
	}
	if d.Effect == EffectRequire {
		if d.Allowed || len(d.Requirements) == 0 {
			return NewError(CodeInvalidDecision, nil)
		}
		for _, obligation := range d.Requirements {
			if !obligation.Valid() {
				return NewError(CodeInvalidObligation, nil)
			}
		}
	}
	return nil
}

// Evaluator validates and evaluates one policy snapshot. It does not persist,
// activate, or infer trust from caller-controlled text.
type Evaluator struct {
	policy Policy
	digest PolicyDigest
}

// NewEvaluator validates and defensively copies a policy snapshot.
func NewEvaluator(policy Policy) (*Evaluator, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	digest, err := policy.Digest()
	if err != nil {
		return nil, err
	}
	return &Evaluator{policy: clonePolicy(policy), digest: digest}, nil
}

// Evaluate applies deny precedence, then require semantics, then allow. A
// missing match uses the policy default, which is deny when omitted.
func (e *Evaluator) Evaluate(ctx context.Context, request EvaluationRequest) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, NewError(CodeParseError, err)
	}
	if err := request.Validate(); err != nil {
		return Decision{}, err
	}
	attrs := cloneAttributes(request.Attributes)
	if attrs == nil {
		attrs = make(map[string]string)
	}
	attrs["action"] = string(request.Action)
	attrs["resource"] = string(request.Resource)
	if request.Provider != "" {
		attrs["provider"] = request.Provider
	}
	matched := make([]Rule, 0, len(e.policy.Rules))
	for _, rule := range e.policy.Rules {
		if matches(rule.When, attrs) {
			matched = append(matched, rule)
		}
	}
	sort.SliceStable(matched, func(i, j int) bool {
		if matched[i].Priority != matched[j].Priority {
			return matched[i].Priority > matched[j].Priority
		}
		return matched[i].ID < matched[j].ID
	})
	for _, rule := range matched {
		if rule.Effect == EffectDeny {
			return Decision{DeniedBy: rule.ID, PolicyDigest: e.digest, Binding: PolicyBinding{Version: e.policy.Version, Digest: e.digest}, Effect: EffectDeny}, nil
		}
	}
	var requirements []Obligation
	for _, rule := range matched {
		if rule.Effect == EffectRequire {
			requirements = append(requirements, rule.Require...)
		}
	}
	if len(requirements) > 0 {
		return Decision{Requirements: uniqueObligations(requirements), PolicyDigest: e.digest, Binding: PolicyBinding{Version: e.policy.Version, Digest: e.digest}, Effect: EffectRequire}, nil
	}
	for _, rule := range matched {
		if rule.Effect == EffectAllow {
			return Decision{Allowed: true, AllowedBy: rule.ID, PolicyDigest: e.digest, Binding: PolicyBinding{Version: e.policy.Version, Digest: e.digest}, Effect: EffectAllow}, nil
		}
	}
	return decisionForEffect(e.policy.Default, e.policy.Version, e.digest)
}

func decisionForEffect(effect Effect, version PolicyVersion, digest PolicyDigest) (Decision, error) {
	if effect == "" {
		effect = EffectDeny
	}
	switch effect {
	case EffectDeny:
		return Decision{PolicyDigest: digest, Binding: PolicyBinding{Version: version, Digest: digest}, Effect: EffectDeny}, nil
	case EffectAllow:
		return Decision{Allowed: true, PolicyDigest: digest, Binding: PolicyBinding{Version: version, Digest: digest}, Effect: EffectAllow}, nil
	default:
		return Decision{}, NewError(CodeInvalidDecision, nil)
	}
}

func matches(when, attrs map[string]string) bool {
	for key, value := range when {
		if attrs[key] != value {
			return false
		}
	}
	return true
}

func uniqueObligations(values []Obligation) []Obligation {
	seen := make(map[Obligation]struct{}, len(values))
	result := make([]Obligation, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; !ok {
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func cloneAttributes(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	clone := make(map[string]string, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}

func clonePolicy(policy Policy) Policy {
	clone := policy
	clone.Rules = make([]Rule, len(policy.Rules))
	for i, rule := range policy.Rules {
		clone.Rules[i] = rule
		clone.Rules[i].When = cloneAttributes(rule.When)
		clone.Rules[i].Require = append([]Obligation(nil), rule.Require...)
	}
	return clone
}

// Validate checks the bounded contract before evaluation or persistence.
func (p Policy) Validate() error {
	if !validIdentifier(string(p.ID)) || p.Version <= 0 {
		return NewError(CodeParseError, nil)
	}
	if p.Default != "" && !p.Default.Valid() {
		return NewError(CodeInvalidDecision, nil)
	}
	if len(p.Rules) > maxRules {
		return NewError(CodeParseError, nil)
	}
	seen := make(map[string]struct{}, len(p.Rules))
	for _, rule := range p.Rules {
		if !validIdentifier(rule.ID) || rule.Description == "" || len(rule.Description) > maxDescription || rule.Effect == "" || !rule.Effect.Valid() {
			return NewError(CodeParseError, nil)
		}
		if _, exists := seen[rule.ID]; exists {
			return NewError(CodeConflict, nil)
		}
		seen[rule.ID] = struct{}{}
		if len(rule.When) > maxAttributes || len(rule.Require) > maxObligations {
			return NewError(CodeParseError, nil)
		}
		for key, value := range rule.When {
			if len(key) == 0 || len(key) > maxAttributeKey || len(value) > maxAttributeValue {
				return NewError(CodeParseError, nil)
			}
		}
		for _, obligation := range rule.Require {
			if !obligation.Valid() || rule.Effect != EffectRequire {
				return NewError(CodeInvalidObligation, nil)
			}
		}
	}
	return nil
}

func (r EvaluationRequest) Validate() error {
	if !validIdentifier(r.SubjectID) || len(r.TaskID) > maxIdentifier || len(r.ChangeID) > maxIdentifier || !validIdentifier(string(r.Action)) || !validResource(string(r.Resource)) {
		return NewError(CodeUnknownAction, nil)
	}
	if len(r.Attributes) > maxAttributes {
		return NewError(CodeParseError, nil)
	}
	for key, value := range r.Attributes {
		if key == "" || len(key) > maxAttributeKey || len(value) > maxAttributeValue {
			return NewError(CodeParseError, nil)
		}
	}
	return nil
}

func validIdentifier(value string) bool {
	if value == "" || len(value) > maxIdentifier || strings.ContainsAny(value, " \t\r\n/") {
		return false
	}
	return true
}

func validResource(value string) bool {
	if value == "" || len(value) > maxAttributeValue {
		return false
	}
	for _, r := range value {
		if r == '\x00' || r == '\r' || r == '\n' || r == '\t' {
			return false
		}
	}
	return true
}
