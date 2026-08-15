package policy

import (
	"bytes"
	"errors"
	"io"

	"go.yaml.in/yaml/v3"
)

const (
	maxIdentifier     = 128
	maxDescription    = 1024
	maxAttributes     = 64
	maxAttributeKey   = 128
	maxAttributeValue = 1024
	maxObligations    = 16
	maxRules          = 256
)

// Parse strictly decodes one YAML or JSON policy document and validates it.
// Unknown fields and trailing documents are rejected before evaluation.
func Parse(data []byte) (Policy, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var policy Policy
	if err := decoder.Decode(&policy); err != nil {
		var typeErr *yaml.TypeError
		if errors.As(err, &typeErr) {
			return Policy{}, NewError(CodeUnknownField, err)
		}
		return Policy{}, NewError(CodeParseError, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return Policy{}, NewError(CodeParseError, nil)
		}
		return Policy{}, NewError(CodeParseError, err)
	}
	if err := policy.Validate(); err != nil {
		return Policy{}, err
	}
	return clonePolicy(policy), nil
}

// ParseEvaluator parses a policy and prepares its immutable evaluator.
func ParseEvaluator(data []byte) (*Evaluator, error) {
	policy, err := Parse(data)
	if err != nil {
		return nil, err
	}
	return NewEvaluator(policy)
}
