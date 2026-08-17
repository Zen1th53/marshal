package gate

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/evidence"
	"github.com/Zen1th53/marshal/internal/policy"
)

func TestObservedGateEvaluationRecordsBoundedMetrics(t *testing.T) {
	metrics := evidence.NewMetricsRecorder()
	digest := policy.PolicyDigest("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	engine, err := NewEngine(EngineConfig{
		PolicyDigest:   digest,
		RequiredChecks: map[GatePoint][]CheckID{GatePointPrePush: {"check"}},
		Checks: map[CheckID]CheckFunc{"check": func(context.Context, CheckRequest) (CheckResult, error) {
			return CheckResult{Status: CheckStatusPass, Reason: CodeAllowed}, nil
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	decision, err := engine.EvaluateObserved(context.Background(), GatePointPrePush, "agent-a09", "repo:a09", metrics)
	if err != nil || !decision.Allowed {
		t.Fatalf("decision=%#v err=%v", decision, err)
	}
	if got := metrics.Snapshot().Success[evidence.MetricOperationGate]; got != 1 {
		t.Fatalf("allow metrics=%d", got)
	}
	if metrics.Snapshot().DurationNanoseconds[evidence.MetricOperationGate] == 0 {
		t.Fatal("gate duration missing")
	}
}

func TestObservedGateMetricsKeepClosedFailureReason(t *testing.T) {
	metrics := evidence.NewMetricsRecorder()
	engine, err := NewEngine(EngineConfig{PolicyDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", RequiredChecks: map[GatePoint][]CheckID{GatePointPrePush: {"missing"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.EvaluateObserved(context.Background(), GatePointPrePush, "agent", "repo", metrics); err == nil {
		t.Fatal("missing check unexpectedly allowed")
	}
	if got := metrics.Snapshot().Invalid[string(CodeUnknownCheck)]; got != 1 {
		t.Fatalf("invalid reason count=%d", got)
	}
}

func BenchmarkEvaluateObserved(b *testing.B) {
	metrics := evidence.NewMetricsRecorder()
	engine, err := NewEngine(EngineConfig{
		PolicyDigest:   "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		RequiredChecks: map[GatePoint][]CheckID{GatePointPrePush: {"check"}},
		Checks: map[CheckID]CheckFunc{"check": func(context.Context, CheckRequest) (CheckResult, error) {
			return CheckResult{Status: CheckStatusPass, Reason: CodeAllowed}, nil
		}},
	})
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := engine.EvaluateObserved(context.Background(), GatePointPrePush, "agent", "repo", metrics); err != nil {
			b.Fatal(err)
		}
	}
}
