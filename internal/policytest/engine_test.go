package policytest

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/policy"
)

type countingEvaluator struct {
	calls    int
	decision policy.Decision
	err      error
}

func (e *countingEvaluator) Evaluate(context.Context, policy.EvaluationRequest) (policy.Decision, error) {
	e.calls++
	return e.decision, e.err
}

func TestRunSuiteExpectedDenyPassesWithoutProductionSideEffect(t *testing.T) {
	p := policy.Policy{ID: "policy-1", Version: 1, Default: policy.EffectDeny}
	digest, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	suite, err := NewSuite(Suite{ID: "suite-1", Cases: []Case{{
		ID: "case-1", Name: "deny by default",
		Given:  Given{Policy: p, Binding: policy.PolicyBinding{Version: 1, Digest: digest, Generation: 1}},
		When:   policy.EvaluationRequest{SubjectID: "subject-1", Action: "read", Resource: "repo"},
		Expect: Expectation{Decision: policy.EffectDeny},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	evaluator := &countingEvaluator{decision: policy.Decision{Effect: policy.EffectDeny, PolicyDigest: digest, Binding: policy.PolicyBinding{Version: 1, Digest: digest}}}
	result, err := RunSuite(context.Background(), suite, evaluator)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Cases) != 1 || result.Cases[0].Result.Status != StatusPass || result.Status != StatusPass {
		t.Fatalf("result = %#v", result)
	}
	if evaluator.calls != 1 {
		t.Fatalf("evaluator calls = %d, want 1", evaluator.calls)
	}
}

func TestRunSuiteTypedExpectationAndBindingValidation(t *testing.T) {
	p := policy.Policy{ID: "policy-1", Version: 1, Default: policy.EffectAllow}
	digest, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	makeSuite := func(expect Expectation) Suite {
		suite, err := NewSuite(Suite{ID: "suite-1", Cases: []Case{{
			ID: "case-1", Name: "typed", Given: Given{Policy: p, Binding: policy.PolicyBinding{Version: 1, Digest: digest, Generation: 3}},
			When: policy.EvaluationRequest{SubjectID: "subject-1", Action: "read", Resource: "repo"}, Expect: expect,
		}}})
		if err != nil {
			t.Fatal(err)
		}
		return suite
	}
	valid := policy.Decision{Allowed: true, Effect: policy.EffectAllow, PolicyDigest: digest, Binding: policy.PolicyBinding{Version: 1, Digest: digest}}
	got, err := RunSuite(context.Background(), makeSuite(Expectation{Decision: policy.EffectAllow}), &countingEvaluator{decision: valid})
	if err != nil || got.Status != StatusPass {
		t.Fatalf("valid allow result=%#v err=%v", got, err)
	}
	wrong := valid
	wrong.PolicyDigest = policy.PolicyDigest("sha256:" + strings.Repeat("0", 64))
	wrong.Binding.Digest = wrong.PolicyDigest
	got, err = RunSuite(context.Background(), makeSuite(Expectation{Decision: policy.EffectAllow}), &countingEvaluator{decision: wrong})
	if err != nil || got.Cases[0].Result.Status != StatusFail {
		t.Fatalf("malformed decision result=%#v err=%v", got, err)
	}
}

func TestRunSuiteExpectedErrorAndUnexpectedEvaluatorError(t *testing.T) {
	p := policy.Policy{ID: "policy-1", Version: 1, Default: policy.EffectDeny}
	digest, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	suite, err := NewSuite(Suite{ID: "suite-1", Cases: []Case{{
		ID: "case-1", Name: "error", Given: Given{Policy: p, Binding: policy.PolicyBinding{Version: 1, Digest: digest, Generation: 1}},
		When: policy.EvaluationRequest{SubjectID: "subject-1", Action: "read", Resource: "repo"}, Expect: Expectation{ExpectedError: policy.CodeParseError},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	expected := &countingEvaluator{err: policy.NewError(policy.CodeParseError, nil)}
	got, err := RunSuite(context.Background(), suite, expected)
	if err != nil || got.Status != StatusPass {
		t.Fatalf("expected error result=%#v err=%v", got, err)
	}
	unexpected := &countingEvaluator{err: errors.New("backend secret MARSHAL_TEST_SECRET_T49_A06_EVAL")}
	allowSuite, err := NewSuite(Suite{ID: "suite-allow", Cases: []Case{{
		ID: "case-1", Name: "unexpected", Given: Given{Policy: p, Binding: policy.PolicyBinding{Version: 1, Digest: digest, Generation: 1}},
		When: policy.EvaluationRequest{SubjectID: "subject-1", Action: "read", Resource: "repo"}, Expect: Expectation{Decision: policy.EffectDeny},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	got, err = RunSuite(context.Background(), allowSuite, unexpected)
	if err != nil || got.Status != StatusError || strings.Contains(got.Cases[0].Result.Diff, "MARSHAL_TEST_SECRET") {
		t.Fatalf("unexpected error result=%#v err=%v", got, err)
	}
}

func TestAggregateResultsUsesFailClosedPrecedence(t *testing.T) {
	results := []TestCaseResult{
		{ID: "a", Result: Result{Name: "a", Status: StatusPass}},
		{ID: "b", Result: Result{Name: "b", Status: StatusSkip, Reason: policy.CodeParseError}},
		{ID: "c", Result: Result{Name: "c", Status: StatusFail}},
	}
	if got := aggregateResults(results); got != StatusFail {
		t.Fatalf("aggregate = %s, want FAIL", got)
	}
	results[2].Result.Status = StatusError
	if got := aggregateResults(results); got != StatusError {
		t.Fatalf("aggregate = %s, want ERROR", got)
	}
}

func TestRunSuiteProviderLabelsDoNotChangeSemantics(t *testing.T) {
	for _, provider := range []string{"Codex", "Claude", "Gemini", "OpenCode"} {
		t.Run(provider, func(t *testing.T) {
			caseInput := validCase(t)
			caseInput.When.Provider = provider
			suite, err := NewSuite(Suite{ID: SuiteID("provider-" + provider), Cases: []Case{caseInput}})
			if err != nil {
				t.Fatal(err)
			}
			digest := suite.Cases[0].Given.Binding.Digest
			evaluator := &countingEvaluator{decision: policy.Decision{
				Allowed:      true,
				Effect:       policy.EffectAllow,
				PolicyDigest: digest,
				AllowedBy:    "allow-read",
				Binding:      policy.PolicyBinding{Version: 1, Digest: digest},
			}}
			result, err := RunSuite(context.Background(), suite, evaluator)
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != StatusPass || evaluator.calls != 1 {
				t.Fatalf("result=%#v calls=%d", result, evaluator.calls)
			}
		})
	}
}
