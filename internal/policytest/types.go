package policytest

import (
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Zen1th53/marshal/internal/policy"
)

const (
	maxSuiteID       = 128
	maxCaseID        = 128
	maxCaseName      = 256
	maxCases         = 1024
	maxMatchedRules  = 128
	maxExpectedOblig = 32
	maxDiff          = 4096
)

type SuiteID string
type CaseID string

// Given binds a case to one exact immutable policy snapshot. A case never
// resolves "latest" and never selects production policy state.
type Given struct {
	Policy  policy.Policy
	Binding policy.PolicyBinding
}

// Expectation contains typed, machine-comparable policy semantics. It is
// observation data only and can never authorize runtime or management work.
type Expectation struct {
	Decision      policy.Effect
	Required      []policy.Obligation
	MatchedRules  []string
	ExpectedError policy.ErrorCode
}

// Case is the declarative given/when/expect contract for one policy test.
type Case struct {
	ID     CaseID
	Name   string
	Given  Given
	When   policy.EvaluationRequest
	Expect Expectation
}

// Suite is an in-memory collection of independently identified cases.
type Suite struct {
	ID    SuiteID
	Cases []Case
}

type ResultStatus string

const (
	StatusPass  ResultStatus = "PASS"
	StatusFail  ResultStatus = "FAIL"
	StatusError ResultStatus = "ERROR"
	StatusSkip  ResultStatus = "SKIP"
)

// Result is a bounded, non-authoritative test outcome.
type Result struct {
	Name   string
	Status ResultStatus
	Diff   string
	Reason policy.ErrorCode
}

func NewCase(input Case) (Case, error) {
	if err := input.Validate(); err != nil {
		return Case{}, err
	}
	return cloneCase(input), nil
}

func NewSuite(input Suite) (Suite, error) {
	if !validBoundedID(string(input.ID), maxSuiteID) || len(input.Cases) > maxCases {
		return Suite{}, NewError(CodeCaseInvalid)
	}
	seen := make(map[CaseID]struct{}, len(input.Cases))
	cloned := make([]Case, len(input.Cases))
	for i, testCase := range input.Cases {
		if _, exists := seen[testCase.ID]; exists {
			return Suite{}, NewError(CodeCaseInvalid)
		}
		seen[testCase.ID] = struct{}{}
		validated, err := NewCase(testCase)
		if err != nil {
			return Suite{}, err
		}
		cloned[i] = validated
	}
	return Suite{ID: input.ID, Cases: cloned}, nil
}

func (c Case) Validate() error {
	if !validBoundedID(string(c.ID), maxCaseID) || !validText(c.Name, maxCaseName) {
		return NewError(CodeCaseInvalid)
	}
	if err := c.Given.Validate(); err != nil {
		return err
	}
	if err := c.When.Validate(); err != nil {
		return NewError(CodeCaseInvalid)
	}
	if err := c.Expect.Validate(); err != nil {
		return err
	}
	return nil
}

func (g Given) Validate() error {
	if err := g.Policy.Validate(); err != nil {
		return NewError(CodeCaseInvalid)
	}
	if err := g.Binding.Validate(); err != nil || g.Binding.Version != g.Policy.Version || g.Binding.Digest == "" {
		return NewError(CodeCaseInvalid)
	}
	digest, err := g.Policy.Digest()
	if err != nil || digest != g.Binding.Digest || g.Binding.Version != g.Policy.Version {
		return NewError(CodeCaseInvalid)
	}
	if g.Binding.Generation > math.MaxInt64 {
		return NewError(CodeCaseInvalid)
	}
	return nil
}

func (e Expectation) Validate() error {
	if e.ExpectedError != "" {
		if !validReasonCode(e.ExpectedError) || e.Decision != "" || len(e.Required) != 0 || len(e.MatchedRules) != 0 {
			return NewError(CodeCaseInvalid)
		}
		return nil
	}
	if !e.Decision.Valid() {
		return NewError(CodeCaseInvalid)
	}
	if len(e.Required) > maxExpectedOblig {
		return NewError(CodeCaseInvalid)
	}
	seenObligations := make(map[policy.Obligation]struct{}, len(e.Required))
	for _, obligation := range e.Required {
		if !obligation.Valid() {
			return NewError(CodeCaseInvalid)
		}
		if _, exists := seenObligations[obligation]; exists {
			return NewError(CodeCaseInvalid)
		}
		seenObligations[obligation] = struct{}{}
	}
	if e.Decision != policy.EffectRequire && len(e.Required) != 0 {
		return NewError(CodeCaseInvalid)
	}
	if len(e.MatchedRules) > maxMatchedRules {
		return NewError(CodeCaseInvalid)
	}
	seenRules := make(map[string]struct{}, len(e.MatchedRules))
	for _, ruleID := range e.MatchedRules {
		if !validBoundedID(ruleID, maxCaseID) {
			return NewError(CodeCaseInvalid)
		}
		if _, exists := seenRules[ruleID]; exists {
			return NewError(CodeCaseInvalid)
		}
		seenRules[ruleID] = struct{}{}
	}
	return nil
}

func (r Result) Validate() error {
	if !validText(r.Name, maxCaseName) || (r.Diff != "" && !validText(r.Diff, maxDiff)) {
		return NewError(CodeCaseInvalid)
	}
	switch r.Status {
	case StatusPass:
		if r.Reason != "" {
			return NewError(CodeCaseInvalid)
		}
	case StatusFail, StatusError:
		if r.Reason != "" && !validReasonCode(r.Reason) {
			return NewError(CodeCaseInvalid)
		}
	case StatusSkip:
		if !validReasonCode(r.Reason) {
			return NewError(CodeCaseInvalid)
		}
	default:
		return NewError(CodeCaseInvalid)
	}
	return nil
}

func validReasonCode(code policy.ErrorCode) bool {
	switch code {
	case policy.CodeParseError, policy.CodeUnknownField, policy.CodeUnknownAction, policy.CodeConflict,
		policy.CodeDeny,
		policy.CodeAuthorizationDenied, policy.CodeAuthorizationAllowed, policy.CodeAuthorizationUnavailable,
		policy.CodeAuthorizationInvalid, policy.CodeAuthorizationStale,
		policy.ErrorCode(CodeCaseInvalid), policy.ErrorCode(CodeExpectationMismatch), policy.ErrorCode(CodeRunInvalid),
		policy.ErrorCode(CodeStateInvalid), policy.ErrorCode(CodeIllegalTransition), policy.ErrorCode(CodeStaleState):
		return true
	default:
		return false
	}
}

func cloneCase(input Case) Case {
	clone := input
	clone.Given.Policy.Rules = make([]policy.Rule, len(input.Given.Policy.Rules))
	for i, rule := range input.Given.Policy.Rules {
		clone.Given.Policy.Rules[i] = rule
		clone.Given.Policy.Rules[i].When = cloneStringMap(rule.When)
		clone.Given.Policy.Rules[i].Require = append([]policy.Obligation(nil), rule.Require...)
	}
	clone.When.Attributes = cloneStringMap(input.When.Attributes)
	clone.Expect.Required = append([]policy.Obligation(nil), input.Expect.Required...)
	clone.Expect.MatchedRules = append([]string(nil), input.Expect.MatchedRules...)
	return clone
}

func validBoundedID(value string, limit int) bool {
	return value != "" && validText(value, limit) && !strings.ContainsAny(value, " \t\r\n/")
}

func validText(value string, limit int) bool {
	if value == "" || len(value) > limit || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func cloneStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	clone := make(map[string]string, len(input))
	for key, value := range input {
		clone[key] = value
	}
	return clone
}
