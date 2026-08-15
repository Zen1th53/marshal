package policy

import (
	"context"
	"testing"
)

func benchmarkPolicy() Policy {
	return Policy{
		ID:      "A09-BENCHMARK",
		Version: 1,
		Default: EffectDeny,
		Rules: []Rule{
			{ID: "allow-read", Description: "allow repository reads", When: map[string]string{"action": "read", "resource": "repo"}, Effect: EffectAllow, Priority: 10},
			{ID: "deny-write", Description: "deny repository writes", When: map[string]string{"action": "write", "resource": "repo"}, Effect: EffectDeny, Priority: 20},
		},
	}
}

func BenchmarkPolicyDigest(b *testing.B) {
	p := benchmarkPolicy()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := p.Digest(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPolicyValidate(b *testing.B) {
	p := benchmarkPolicy()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := p.Validate(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPolicyEvaluate(b *testing.B) {
	evaluator, err := NewEvaluator(benchmarkPolicy())
	if err != nil {
		b.Fatal(err)
	}
	request := EvaluationRequest{SubjectID: "subject", TaskID: "task", Action: "read", Resource: "repo"}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := evaluator.Evaluate(context.Background(), request); err != nil {
			b.Fatal(err)
		}
	}
}
