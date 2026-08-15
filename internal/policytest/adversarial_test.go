package policytest

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/policy"
)

func TestRunSuiteUnknownEvaluatorErrorUsesStableReasonWithoutSecret(t *testing.T) {
	const marker = "MARSHAL_TEST_SECRET_T49_A07_EVALUATOR_7f2a"
	input := validCase(t)
	suite, err := NewSuite(Suite{ID: "suite-a07-error", Cases: []Case{input}})
	if err != nil {
		t.Fatal(err)
	}

	result, err := RunSuite(context.Background(), suite, &countingEvaluator{
		decision: policy.Decision{},
		err:      errors.New("backend failure: " + marker),
	})
	if err != nil {
		t.Fatal(err)
	}
	caseResult := result.Cases[0].Result
	if result.Status != StatusError || caseResult.Status != StatusError {
		t.Fatalf("result = %#v", result)
	}
	if caseResult.Reason != policy.ErrorCode(CodeCaseInvalid) {
		t.Fatalf("reason = %q, want %q", caseResult.Reason, CodeCaseInvalid)
	}
	if strings.Contains(caseResult.Diff, marker) {
		t.Fatalf("secret marker leaked in result diff: %q", caseResult.Diff)
	}
}

func TestRunSuiteUnknownTypedEvaluatorErrorIsSanitized(t *testing.T) {
	const marker = "MARSHAL_TEST_SECRET_T49_A07_TYPED_7f2a"
	input := validCase(t)
	suite, err := NewSuite(Suite{ID: "suite-a07-typed", Cases: []Case{input}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := RunSuite(context.Background(), suite, &countingEvaluator{
		err: policy.NewError(policy.ErrorCode(marker), nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	got := result.Cases[0].Result
	if got.Reason != policy.ErrorCode(CodeCaseInvalid) || strings.Contains(string(got.Reason), marker) {
		t.Fatalf("result reason = %q", got.Reason)
	}
}

func TestRunSuiteRejectsMalformedActionBeforeEvaluation(t *testing.T) {
	input := validCase(t)
	input.When.Action = ""
	evaluator := &countingEvaluator{}
	if _, err := RunSuite(context.Background(), Suite{ID: "suite-a07-action", Cases: []Case{input}}, evaluator); !errors.Is(err, ErrCaseInvalid) {
		t.Fatalf("malformed action error = %v", err)
	}
	if evaluator.calls != 0 {
		t.Fatalf("evaluator calls = %d, want 0", evaluator.calls)
	}
}

func TestPolicyTestContractsRejectUnknownErrorCodes(t *testing.T) {
	input := validCase(t)
	input.Expect = Expectation{ExpectedError: policy.ErrorCode("POLICYTEST_FORGED_ERROR")}
	if _, err := NewCase(input); !errors.Is(err, ErrCaseInvalid) {
		t.Fatalf("unknown expected error = %v", err)
	}
	if err := (Result{Name: "case-1", Status: StatusFail, Reason: policy.ErrorCode("POLICYTEST_FORGED_ERROR")}).Validate(); !errors.Is(err, ErrCaseInvalid) {
		t.Fatalf("unknown result reason = %v", err)
	}
}

func TestRunSuiteInvertedExpectationHasStableMinimalDiff(t *testing.T) {
	input := validCase(t)
	input.Expect.Decision = policy.EffectDeny
	suite, err := NewSuite(Suite{ID: "suite-a07-diff", Cases: []Case{input}})
	if err != nil {
		t.Fatal(err)
	}
	digest := suite.Cases[0].Given.Binding.Digest
	result, err := RunSuite(context.Background(), suite, &countingEvaluator{decision: policy.Decision{
		Allowed: true, Effect: policy.EffectAllow, PolicyDigest: digest,
		Binding: policy.PolicyBinding{Version: 1, Digest: digest},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got := result.Cases[0].Result
	if got.Status != StatusFail || got.Reason != policy.ErrorCode(CodeExpectationMismatch) || got.Diff != "expected effect deny, got allow" {
		t.Fatalf("result = %#v", got)
	}
}

func TestRunSuiteCanonicalOrderAndRepeatAreStable(t *testing.T) {
	first := validCase(t)
	second := first
	second.ID, second.Name = "case-2", "second"
	suite, err := NewSuite(Suite{ID: "suite-a07-deterministic", Cases: []Case{second, first}})
	if err != nil {
		t.Fatal(err)
	}
	digest := suite.Cases[0].Given.Binding.Digest
	evaluator := &countingEvaluator{decision: policy.Decision{
		Allowed: true, Effect: policy.EffectAllow, PolicyDigest: digest,
		Binding: policy.PolicyBinding{Version: 1, Digest: digest},
	}}
	one, err := RunSuite(context.Background(), suite, evaluator)
	if err != nil {
		t.Fatal(err)
	}
	evaluator.calls = 0
	two, err := RunSuite(context.Background(), suite, evaluator)
	if err != nil {
		t.Fatal(err)
	}
	if one.Status != two.Status || len(one.Cases) != len(two.Cases) || one.Cases[0].ID != "case-1" || one.Cases[1].ID != "case-2" || one.Cases[0].Result != two.Cases[0].Result || one.Cases[1].Result != two.Cases[1].Result {
		t.Fatalf("first=%#v second=%#v", one, two)
	}
}

func FuzzRunSuiteHostileRequestNeverPanics(f *testing.F) {
	f.Add("read", "repo", "subject-1", "safe")
	f.Add("", "repo", "subject-1", "safe")
	f.Add("read", string([]byte{'r', 0xff}), "subject-1", "safe")
	f.Fuzz(func(t *testing.T, action, resource, subject, attribute string) {
		input := validCase(t)
		input.When.Action = policy.Action(action)
		input.When.Resource = policy.Resource(resource)
		input.When.SubjectID = subject
		input.When.Attributes = map[string]string{"fixture": attribute}
		_, _ = RunSuite(context.Background(), Suite{ID: "suite-a07-fuzz", Cases: []Case{input}}, &countingEvaluator{})
	})
}
