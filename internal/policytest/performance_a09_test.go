package policytest

import (
	"context"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/Zen1th53/marshal/internal/policy"
)

type a09BenchmarkEvaluator struct {
	digest policy.PolicyDigest
	calls  atomic.Int64
}

func (e *a09BenchmarkEvaluator) Evaluate(context.Context, policy.EvaluationRequest) (policy.Decision, error) {
	e.calls.Add(1)
	return policy.Decision{
		Allowed: true, Effect: policy.EffectAllow, PolicyDigest: e.digest,
		Binding: policy.PolicyBinding{Version: 1, Digest: e.digest},
	}, nil
}

func a09BenchmarkSuite(b testing.TB, size int) (Suite, policy.PolicyDigest) {
	b.Helper()
	p := policy.Policy{ID: "a09-benchmark-policy", Version: 1, Default: policy.EffectAllow}
	digest, err := p.Digest()
	if err != nil {
		b.Fatal(err)
	}
	cases := make([]Case, size)
	for i := range cases {
		cases[i] = Case{
			ID:     CaseID(fmt.Sprintf("case-%04d", i)),
			Name:   "benchmark case",
			Given:  Given{Policy: p, Binding: policy.PolicyBinding{Version: 1, Digest: digest}},
			When:   policy.EvaluationRequest{SubjectID: "subject", Action: "read", Resource: "repo"},
			Expect: Expectation{Decision: policy.EffectAllow},
		}
	}
	suite, err := NewSuite(Suite{ID: "a09-benchmark-suite", Cases: cases})
	if err != nil {
		b.Fatal(err)
	}
	return suite, digest
}

func BenchmarkA09RunSuite(b *testing.B) {
	for _, size := range []int{1, 100, 1000} {
		b.Run(fmt.Sprintf("cases=%d", size), func(b *testing.B) {
			suite, digest := a09BenchmarkSuite(b, size)
			evaluator := &a09BenchmarkEvaluator{digest: digest}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := RunSuite(context.Background(), suite, evaluator); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestA09LargePureSuiteIsDeterministicAndComplete(t *testing.T) {
	suite, digest := a09BenchmarkSuite(t, 1000)
	firstEvaluator := &a09BenchmarkEvaluator{digest: digest}
	first, err := RunSuite(context.Background(), suite, firstEvaluator)
	if err != nil {
		t.Fatal(err)
	}
	secondEvaluator := &a09BenchmarkEvaluator{digest: digest}
	second, err := RunSuite(context.Background(), suite, secondEvaluator)
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != StatusPass || len(first.Cases) != 1000 || firstEvaluator.calls.Load() != 1000 {
		t.Fatalf("first result status=%s cases=%d calls=%d", first.Status, len(first.Cases), firstEvaluator.calls.Load())
	}
	if secondEvaluator.calls.Load() != 1000 || !reflect.DeepEqual(first, second) {
		t.Fatalf("repeated result is not semantically identical: first=%#v second=%#v calls=%d", first, second, secondEvaluator.calls.Load())
	}
}
