package policytest

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/Zen1th53/marshal/internal/policy"
)

const maxSuiteFileSize = 1 << 20

type suiteDocument struct {
	ID    string         `json:"id"`
	Cases []caseDocument `json:"cases"`
}

type caseDocument struct {
	ID     string              `json:"id"`
	Name   string              `json:"name"`
	Given  givenDocument       `json:"given"`
	When   evaluationDocument  `json:"when"`
	Expect expectationDocument `json:"expect"`
}

type givenDocument struct {
	Policy  policyDocument  `json:"policy"`
	Binding bindingDocument `json:"binding"`
}

type bindingDocument struct {
	Version    policy.PolicyVersion `json:"version"`
	Digest     policy.PolicyDigest  `json:"digest"`
	Generation uint64               `json:"generation"`
}

type policyDocument struct {
	ID      policy.PolicyID      `json:"id"`
	Version policy.PolicyVersion `json:"version"`
	Default policy.Effect        `json:"default"`
	Rules   []ruleDocument       `json:"rules"`
}

type ruleDocument struct {
	ID          string              `json:"id"`
	Description string              `json:"description"`
	When        map[string]string   `json:"when"`
	Effect      policy.Effect       `json:"effect"`
	Require     []policy.Obligation `json:"require"`
	Priority    int                 `json:"priority"`
}

type evaluationDocument struct {
	SubjectID  string            `json:"subject_id"`
	TaskID     string            `json:"task_id"`
	ChangeID   string            `json:"change_id"`
	Action     policy.Action     `json:"action"`
	Resource   policy.Resource   `json:"resource"`
	Provider   string            `json:"provider"`
	Attributes map[string]string `json:"attributes"`
}

type expectationDocument struct {
	Decision      policy.Effect       `json:"decision"`
	Required      []policy.Obligation `json:"required"`
	MatchedRules  []string            `json:"matched_rules"`
	ExpectedError policy.ErrorCode    `json:"expected_error"`
}

// ParseJSONSuite strictly parses the canonical CI suite format. It accepts one
// JSON document only and returns bounded, validated policy-test values.
func ParseJSONSuite(data []byte) (Suite, error) {
	if len(data) == 0 || len(data) > maxSuiteFileSize {
		return Suite{}, ErrCaseInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var document suiteDocument
	if err := decoder.Decode(&document); err != nil {
		return Suite{}, ErrCaseInvalid
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Suite{}, ErrCaseInvalid
	}

	suite := Suite{ID: SuiteID(document.ID), Cases: make([]Case, len(document.Cases))}
	for i, input := range document.Cases {
		rules := make([]policy.Rule, len(input.Given.Policy.Rules))
		for j, rule := range input.Given.Policy.Rules {
			rules[j] = policy.Rule{
				ID: rule.ID, Description: rule.Description, When: rule.When,
				Effect: rule.Effect, Require: rule.Require, Priority: rule.Priority,
			}
		}
		suite.Cases[i] = Case{
			ID: CaseID(input.ID), Name: input.Name,
			Given: Given{
				Policy:  policy.Policy{ID: input.Given.Policy.ID, Version: input.Given.Policy.Version, Default: input.Given.Policy.Default, Rules: rules},
				Binding: policy.PolicyBinding{Version: input.Given.Binding.Version, Digest: input.Given.Binding.Digest, Generation: input.Given.Binding.Generation},
			},
			When: policy.EvaluationRequest{
				SubjectID: input.When.SubjectID, TaskID: input.When.TaskID, ChangeID: input.When.ChangeID,
				Action: input.When.Action, Resource: input.When.Resource, Provider: input.When.Provider, Attributes: input.When.Attributes,
			},
			Expect: Expectation{
				Decision: input.Expect.Decision, Required: input.Expect.Required,
				MatchedRules: input.Expect.MatchedRules, ExpectedError: input.Expect.ExpectedError,
			},
		}
	}
	return NewSuite(suite)
}
