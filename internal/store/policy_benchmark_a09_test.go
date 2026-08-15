package store

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/evidence"
	"github.com/Zen1th53/marshal/internal/policy"
)

func BenchmarkPolicyGet(b *testing.B) {
	ctx := context.Background()
	st := openBenchmarkPolicyStore(b)
	record := benchmarkPolicyRecord("A09-GET")
	if err := st.PutPolicy(ctx, record); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := st.GetPolicy(ctx, record.Policy.ID, record.Policy.Version); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPolicyGetActive(b *testing.B) {
	ctx := context.Background()
	st := openBenchmarkPolicyStore(b)
	record := benchmarkPolicyRecord("A09-ACTIVE")
	record.State = policy.StateActive
	if err := st.PutPolicy(ctx, record); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := st.GetActivePolicy(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkPolicyRecord(id string) PolicyRecord {
	p := policy.Policy{ID: policy.PolicyID(id), Version: 1, Default: policy.EffectDeny}
	digest, _ := p.Digest()
	return PolicyRecord{Policy: p, Binding: policy.PolicyBinding{Version: 1, Digest: digest, Generation: 1}}
}

func openBenchmarkPolicyStore(b *testing.B) *Store {
	b.Helper()
	st, err := OpenWithObservability(context.Background(), b.TempDir()+"/policy.db", evidence.NewStrictSanitizer(evidence.SanitizerConfig{}), nil, nil)
	if err != nil {
		b.Fatal(err)
	}
	if err := st.Migrate(context.Background()); err != nil {
		st.Close()
		b.Fatal(err)
	}
	b.Cleanup(func() { st.Close() })
	return st
}
