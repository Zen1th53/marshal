package policytest

import (
	"time"

	"github.com/Zen1th53/marshal/internal/policy"
)

const maxTestRunCases = maxCases

// TestCaseResult is the bounded, non-authoritative result for one case in a
// persisted test run. It contains no policy fixture or raw evaluator output.
type TestCaseResult struct {
	ID     CaseID
	Result Result
}

// TestRun is a durable projection of policy-test outcomes. The referenced
// policy binding is exact, but the run never carries or changes T48 policy
// authority.
type TestRun struct {
	ID             string
	PolicyID       policy.PolicyID
	Binding        policy.PolicyBinding
	TestFileDigest policy.PolicyDigest
	Cases          []TestCaseResult
	CreatedAt      time.Time
}

func (r TestRun) Validate() error {
	if !validBoundedID(r.ID, maxSuiteID) || !validBoundedID(string(r.PolicyID), maxSuiteID) {
		return NewError(CodeRunInvalid)
	}
	if err := r.Binding.Validate(); err != nil || r.TestFileDigest.Validate() != nil {
		return NewError(CodeRunInvalid)
	}
	if len(r.Cases) > maxTestRunCases {
		return NewError(CodeRunInvalid)
	}
	seen := make(map[CaseID]struct{}, len(r.Cases))
	for _, result := range r.Cases {
		if _, exists := seen[result.ID]; exists || !validBoundedID(string(result.ID), maxCaseID) || string(result.Result.Name) != string(result.ID) {
			return NewError(CodeRunInvalid)
		}
		if err := result.Result.Validate(); err != nil {
			return NewError(CodeRunInvalid)
		}
		seen[result.ID] = struct{}{}
	}
	return nil
}

func CloneTestRun(input TestRun) TestRun {
	clone := input
	clone.Cases = append([]TestCaseResult(nil), input.Cases...)
	return clone
}
