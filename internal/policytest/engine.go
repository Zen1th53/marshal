package policytest

import (
	"context"
	"fmt"
	"sort"

	"github.com/Zen1th53/marshal/internal/policy"
)

// Evaluator is the narrow, provider-neutral T48 evaluation contract consumed
// by the policy-test runner. It never performs a privileged action.
type Evaluator interface {
	Evaluate(context.Context, policy.EvaluationRequest) (policy.Decision, error)
}

// RunResult is a bounded, non-authoritative observation of one suite run.
type RunResult struct {
	Cases  []TestCaseResult
	Status ResultStatus
}

// RunRequest carries the exact durable run identity and authenticated context
// required by the store integration. Fixture fields never provide authority.
type RunRequest struct {
	RunID          string
	Suite          Suite
	TestFileDigest policy.PolicyDigest
	SubjectID      string
	SessionID      string
	TaskID         string
	ChangeID       string
	Evaluator      Evaluator
	Authorizer     Authorizer
}

// RunSuite evaluates every case once in canonical CaseID order. It compares
// typed T48 decisions and stable error codes only; evaluator error text is
// deliberately excluded from results.
func RunSuite(ctx context.Context, suite Suite, evaluator Evaluator) (RunResult, error) {
	if evaluator == nil {
		return RunResult{}, policy.ErrAuthorizationUnavailable
	}
	validated, err := NewSuite(suite)
	if err != nil {
		return RunResult{}, err
	}
	if len(validated.Cases) == 0 {
		return RunResult{}, ErrCaseInvalid
	}
	if err := ctx.Err(); err != nil {
		return RunResult{}, err
	}
	sort.Slice(validated.Cases, func(i, j int) bool { return validated.Cases[i].ID < validated.Cases[j].ID })
	results := make([]TestCaseResult, 0, len(validated.Cases))
	for _, testCase := range validated.Cases {
		if err := ctx.Err(); err != nil {
			return RunResult{}, err
		}
		result := evaluateCase(ctx, testCase, evaluator)
		results = append(results, TestCaseResult{ID: testCase.ID, Result: result})
	}
	return RunResult{Cases: results, Status: aggregateResults(results)}, nil
}

func evaluateCase(ctx context.Context, testCase Case, evaluator Evaluator) Result {
	decision, evalErr := evaluator.Evaluate(ctx, testCase.When)
	if testCase.Expect.ExpectedError != "" {
		if evalErr != nil && policy.ReasonCode(evalErr) == testCase.Expect.ExpectedError {
			return Result{Name: string(testCase.ID), Status: StatusPass}
		}
		if evalErr != nil {
			return Result{Name: string(testCase.ID), Status: StatusFail, Reason: policy.ErrorCode(CodeExpectationMismatch), Diff: "expected error code did not match"}
		}
		return Result{Name: string(testCase.ID), Status: StatusFail, Reason: policy.ErrorCode(CodeExpectationMismatch), Diff: "expected error, evaluator returned decision"}
	}
	if evalErr != nil {
		reason := policy.ReasonCode(evalErr)
		if !validReasonCode(reason) {
			reason = policy.ErrorCode(CodeCaseInvalid)
		}
		return Result{Name: string(testCase.ID), Status: StatusError, Reason: reason}
	}
	if err := decision.Validate(); err != nil {
		return Result{Name: string(testCase.ID), Status: StatusError, Reason: policy.ReasonCode(err)}
	}
	if !decisionBindingMatches(testCase.Given.Binding, decision.Binding, decision.PolicyDigest) {
		return Result{Name: string(testCase.ID), Status: StatusFail, Reason: policy.ErrorCode(CodeExpectationMismatch), Diff: "decision binding mismatch"}
	}
	if decision.Effect != testCase.Expect.Decision {
		return Result{Name: string(testCase.ID), Status: StatusFail, Reason: policy.ErrorCode(CodeExpectationMismatch), Diff: fmt.Sprintf("expected effect %s, got %s", testCase.Expect.Decision, decision.Effect)}
	}
	if !sameObligations(testCase.Expect.Required, decision.Requirements) {
		return Result{Name: string(testCase.ID), Status: StatusFail, Reason: policy.ErrorCode(CodeExpectationMismatch), Diff: "obligation set mismatch"}
	}
	if !sameMatchedRules(testCase.Expect.MatchedRules, decision) {
		return Result{Name: string(testCase.ID), Status: StatusFail, Reason: policy.ErrorCode(CodeExpectationMismatch), Diff: "matched rule set mismatch"}
	}
	return Result{Name: string(testCase.ID), Status: StatusPass}
}

func decisionBindingMatches(expected, actual policy.PolicyBinding, digest policy.PolicyDigest) bool {
	if actual.Version != expected.Version || actual.Digest != expected.Digest || digest != expected.Digest {
		return false
	}
	// T48's pure evaluator does not have the external lifecycle generation;
	// non-zero generations from adapters must nevertheless match exactly.
	return actual.Generation == 0 || actual.Generation == expected.Generation
}

func sameObligations(expected, actual []policy.Obligation) bool {
	expected = append([]policy.Obligation(nil), expected...)
	actual = append([]policy.Obligation(nil), actual...)
	sort.Slice(expected, func(i, j int) bool { return expected[i] < expected[j] })
	sort.Slice(actual, func(i, j int) bool { return actual[i] < actual[j] })
	if len(expected) != len(actual) {
		return false
	}
	for i := range expected {
		if expected[i] != actual[i] {
			return false
		}
	}
	return true
}

func sameMatchedRules(expected []string, decision policy.Decision) bool {
	if len(expected) == 0 {
		return true
	}
	if len(expected) != 1 {
		return false
	}
	actual := decision.AllowedBy
	if actual == "" {
		actual = decision.DeniedBy
	}
	return expected[0] == actual
}

func aggregateResults(results []TestCaseResult) ResultStatus {
	status := StatusPass
	for _, result := range results {
		switch result.Result.Status {
		case StatusError:
			return StatusError
		case StatusFail:
			status = StatusFail
		case StatusSkip:
			if status == StatusPass {
				status = StatusSkip
			}
		}
	}
	return status
}
