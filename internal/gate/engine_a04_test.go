package gate

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/policy"
)

func TestEngineUnknownRequiredCheckFailsClosed(t *testing.T) {
	engine, err := NewEngine(EngineConfig{
		PolicyDigest:   policy.PolicyDigest("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		RequiredChecks: map[GatePoint][]CheckID{GatePointPrePush: {"secret-scan"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	decision, err := engine.Evaluate(context.Background(), GatePointPrePush, "agent-a04", "repo:a04")
	if !errors.Is(err, ErrUnknownCheck) || decision.Allowed {
		t.Fatalf("decision=%#v err=%v", decision, err)
	}
}

func TestEngineRejectsStaleEvidenceAndSelfVerification(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	digest := policy.PolicyDigest("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	stale := now.Add(-time.Minute)
	engine, err := NewEngine(EngineConfig{
		PolicyDigest: digest, Clock: func() time.Time { return now },
		RequiredChecks: map[GatePoint][]CheckID{GatePointPreMerge: {"test-evidence"}},
		Checks: map[CheckID]CheckFunc{"test-evidence": func(context.Context, CheckRequest) (CheckResult, error) {
			return CheckResult{Status: CheckStatusPass, EvidenceExpiresAt: &stale}, nil
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	decision, err := engine.Evaluate(context.Background(), GatePointPreMerge, "agent-a04", "change:a04")
	if !errors.Is(err, ErrStaleEvidence) || decision.Allowed || decision.State != DecisionStateDenied {
		t.Fatalf("stale decision=%#v err=%v", decision, err)
	}

	engine, err = NewEngine(EngineConfig{
		PolicyDigest: digest, Clock: func() time.Time { return now }, RequireIndependentCheck: true,
		RequiredChecks: map[GatePoint][]CheckID{GatePointPreMerge: {"self-check"}},
		Checks: map[CheckID]CheckFunc{"self-check": func(context.Context, CheckRequest) (CheckResult, error) {
			return CheckResult{Status: CheckStatusPass, VerifierID: "agent-a04"}, nil
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	decision, err = engine.Evaluate(context.Background(), GatePointPreMerge, "agent-a04", "change:a04")
	if err == nil || decision.Allowed || decision.State != DecisionStateDenied {
		t.Fatalf("self-verification decision=%#v err=%v", decision, err)
	}
}

func TestEngineSnapshotsPolicyDigestAndDoesNotExecuteAction(t *testing.T) {
	digest := policy.PolicyDigest("sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	actions := 0
	engine, err := NewEngine(EngineConfig{
		PolicyDigest:   digest,
		RequiredChecks: map[GatePoint][]CheckID{GatePointPreExecution: {"allow-check"}},
		Checks: map[CheckID]CheckFunc{"allow-check": func(_ context.Context, request CheckRequest) (CheckResult, error) {
			if request.PolicyDigest != digest {
				t.Fatalf("digest=%q want=%q", request.PolicyDigest, digest)
			}
			return CheckResult{Status: CheckStatusPass, Reason: CodeAllowed}, nil
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	decision, err := engine.Evaluate(context.Background(), GatePointPreExecution, "agent-a04", "repo:a04")
	if err != nil || !decision.Allowed || decision.PolicyDigest != digest {
		t.Fatalf("decision=%#v err=%v", decision, err)
	}
	if actions != 0 {
		t.Fatalf("gate executed action count=%d", actions)
	}
}
