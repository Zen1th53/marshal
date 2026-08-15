package policytest

import (
	"errors"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/policy"
)

func TestCaseRejectsAmbiguousPolicyBinding(t *testing.T) {
	p := policy.Policy{ID: "policy-1", Version: 1, Default: policy.EffectDeny}
	digest, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewCase(Case{
		ID: "case-1", Name: "denies by default",
		Given:  Given{Policy: p, Binding: policy.PolicyBinding{Version: 2, Digest: digest, Generation: 1}},
		When:   policy.EvaluationRequest{SubjectID: "subject-1", Action: "read", Resource: "repo"},
		Expect: Expectation{Decision: policy.EffectDeny},
	})
	if !errors.Is(err, ErrCaseInvalid) {
		t.Fatalf("error = %v, want ErrCaseInvalid", err)
	}
}

func validCase(t *testing.T) Case {
	t.Helper()
	p := policy.Policy{ID: "policy-1", Version: 1, Default: policy.EffectDeny,
		Rules: []policy.Rule{{ID: "allow-read", Description: "read", Effect: policy.EffectAllow,
			When: map[string]string{"action": "read"}}}}
	digest, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return Case{
		ID: "case-1", Name: "read is allowed",
		Given:  Given{Policy: p, Binding: policy.PolicyBinding{Version: 1, Digest: digest, Generation: 1}},
		When:   policy.EvaluationRequest{SubjectID: "subject-1", Action: "read", Resource: "repo"},
		Expect: Expectation{Decision: policy.EffectAllow, MatchedRules: []string{"allow-read"}},
	}
}

func TestNewSuiteAcceptsValidCaseAndRejectsDuplicateIDs(t *testing.T) {
	first := validCase(t)
	second := first
	second.Name = "duplicate"
	if _, err := NewSuite(Suite{ID: "suite-1", Cases: []Case{first}}); err != nil {
		t.Fatalf("valid suite rejected: %v", err)
	}
	if _, err := NewSuite(Suite{ID: "suite-1", Cases: []Case{first, second}}); !errors.Is(err, ErrCaseInvalid) {
		t.Fatalf("duplicate case error = %v", err)
	}
}

func TestExpectationRejectsUnknownEffectAndObligation(t *testing.T) {
	caseInput := validCase(t)
	caseInput.Expect.Decision = policy.Effect("permit")
	if _, err := NewCase(caseInput); !errors.Is(err, ErrCaseInvalid) {
		t.Fatalf("unknown effect error = %v", err)
	}
	caseInput = validCase(t)
	caseInput.Expect.Decision = policy.EffectRequire
	caseInput.Expect.Required = []policy.Obligation{policy.Obligation("REQUIRE_ADMIN")}
	if _, err := NewCase(caseInput); !errors.Is(err, ErrCaseInvalid) {
		t.Fatalf("unknown obligation error = %v", err)
	}
}

func TestExpectationSupportsTypedNegativeError(t *testing.T) {
	caseInput := validCase(t)
	caseInput.Expect = Expectation{ExpectedError: policy.CodeParseError}
	if _, err := NewCase(caseInput); err != nil {
		t.Fatalf("typed negative expectation rejected: %v", err)
	}
}

func TestResultStatusesRejectUnknownAndRequireSkipReason(t *testing.T) {
	if !errors.Is(NewError(CodeExpectationMismatch), ErrExpectationMismatch) || ReasonCode(ErrExpectationMismatch) != CodeExpectationMismatch {
		t.Fatal("expectation mismatch error is not machine-readable")
	}
	if err := (Result{Name: "case-1", Status: StatusPass}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (Result{Name: "case-1", Status: StatusSkip}).Validate(); !errors.Is(err, ErrCaseInvalid) {
		t.Fatalf("skip without reason = %v", err)
	}
	if err := (Result{Name: "case-1", Status: ResultStatus("SUCCESS")}).Validate(); !errors.Is(err, ErrCaseInvalid) {
		t.Fatalf("unknown status = %v", err)
	}
	if err := (Result{Name: "case-1", Status: StatusSkip, Reason: policy.CodeParseError}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestNewCaseDefensivelyCopiesFixture(t *testing.T) {
	input := validCase(t)
	input.When.Attributes = map[string]string{"tenant": "one"}
	input.Expect.Required = []policy.Obligation{policy.ObligationApproval}
	input.Expect.Decision = policy.EffectRequire
	validated, err := NewCase(input)
	if err != nil {
		t.Fatal(err)
	}
	input.When.Attributes["tenant"] = "two"
	input.Expect.Required[0] = policy.ObligationVerification
	input.Given.Policy.Rules[0].When["action"] = "write"
	if validated.When.Attributes["tenant"] != "one" || validated.Expect.Required[0] != policy.ObligationApproval || validated.Given.Policy.Rules[0].When["action"] != "read" {
		t.Fatal("validated case aliases caller-owned fixture data")
	}
}

func TestNewCaseIsProviderNeutralAndErrorsAreSecretSafe(t *testing.T) {
	marker := "MARSHAL_TEST_SECRET_T49_A01_7f3a"
	for _, provider := range []string{"Codex", "Claude", "Gemini", "OpenCode", "root"} {
		input := validCase(t)
		input.When.Provider = provider
		if _, err := NewCase(input); err != nil {
			t.Fatalf("provider %q rejected: %v", provider, err)
		}
	}
	input := validCase(t)
	input.ID = CaseID(marker + " ")
	_, err := NewCase(input)
	if !errors.Is(err, ErrCaseInvalid) || strings.Contains(err.Error(), marker) {
		t.Fatalf("secret-bearing error = %v", err)
	}
}

func TestNewSuiteRejectsUnboundedFixture(t *testing.T) {
	input := validCase(t)
	input.Name = strings.Repeat("x", maxCaseName+1)
	if _, err := NewSuite(Suite{ID: "suite-1", Cases: []Case{input}}); !errors.Is(err, ErrCaseInvalid) {
		t.Fatalf("oversized case accepted: %v", err)
	}
	tooMany := make([]Case, maxCases+1)
	for i := range tooMany {
		tooMany[i] = input
		tooMany[i].ID = CaseID("case-" + string(rune('a'+i%26)) + string(rune(i/26)))
		tooMany[i].Name = "bounded"
	}
	if _, err := NewSuite(Suite{ID: "suite-1", Cases: tooMany}); !errors.Is(err, ErrCaseInvalid) {
		t.Fatalf("oversized suite accepted: %v", err)
	}
}
