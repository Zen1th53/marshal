package app

import (
	"context"

	"github.com/Zen1th53/marshal/internal/policy"
	"github.com/Zen1th53/marshal/internal/policytest"
)

const maxPolicyTestFileSize = 1 << 20

type PolicyTestReport struct {
	Status       policytest.ResultStatus `json:"status"`
	PolicyDigest policy.PolicyDigest     `json:"policy_digest"`
	Cases        []PolicyTestCaseReport  `json:"cases"`
}

type PolicyTestCaseReport struct {
	ID     policytest.CaseID       `json:"id"`
	Status policytest.ResultStatus `json:"status"`
	Diff   string                  `json:"diff,omitempty"`
	Reason policy.ErrorCode        `json:"reason,omitempty"`
}

// runPolicyTestData evaluates an already-read declarative suite.
func runPolicyTestData(ctx context.Context, data []byte) (PolicyTestReport, error) {
	if len(data) > maxPolicyTestFileSize {
		return PolicyTestReport{}, policytest.ErrCaseInvalid
	}
	suite, err := policytest.ParseJSONSuite(data)
	if err != nil {
		return PolicyTestReport{}, err
	}
	if len(suite.Cases) == 0 {
		return PolicyTestReport{}, policytest.ErrCaseInvalid
	}
	first := suite.Cases[0]
	firstDigest, err := first.Given.Policy.Digest()
	if err != nil || firstDigest != first.Given.Binding.Digest {
		return PolicyTestReport{}, policytest.ErrCaseInvalid
	}
	for _, testCase := range suite.Cases[1:] {
		digest, digestErr := testCase.Given.Policy.Digest()
		if digestErr != nil || testCase.Given.Policy.ID != first.Given.Policy.ID || testCase.Given.Binding != first.Given.Binding || digest != firstDigest {
			return PolicyTestReport{}, policytest.ErrCaseInvalid
		}
	}
	evaluator, err := policy.NewEvaluator(first.Given.Policy)
	if err != nil {
		return PolicyTestReport{}, err
	}
	result, err := policytest.RunSuite(ctx, suite, evaluator)
	if err != nil {
		return PolicyTestReport{}, err
	}
	report := PolicyTestReport{Status: result.Status, PolicyDigest: firstDigest, Cases: make([]PolicyTestCaseReport, len(result.Cases))}
	for i, caseResult := range result.Cases {
		report.Cases[i] = PolicyTestCaseReport{ID: caseResult.ID, Status: caseResult.Result.Status, Diff: caseResult.Result.Diff, Reason: caseResult.Result.Reason}
	}
	return report, nil
}
