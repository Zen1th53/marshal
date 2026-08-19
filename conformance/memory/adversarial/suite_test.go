package adversarial_test

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/conformance/memory/adversarial"
)

func TestT135AdversarialChaosMemorySuite(t *testing.T) {
	runner := adversarial.NewSuiteRunner()
	ctx := context.Background()

	report, err := runner.RunAdversarialSuite(ctx)
	if err != nil {
		t.Fatalf("RunAdversarialSuite: %v", err)
	}

	if !report.AllPassed {
		t.Fatalf("adversarial test suite failed: %+v", report)
	}

	if report.ACLLeaks != 0 {
		t.Fatalf("CRITICAL: detected %d cross-tenant ACL leaks", report.ACLLeaks)
	}

	if report.SecretLeaks != 0 {
		t.Fatalf("CRITICAL: detected %d secret leaks", report.SecretLeaks)
	}

	if report.InjectionEscapes != 0 {
		t.Fatalf("CRITICAL: detected %d prompt injection escapes", report.InjectionEscapes)
	}
}
