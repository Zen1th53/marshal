package app

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/evidence"
	"github.com/Zen1th53/marshal/internal/policy"
	"github.com/Zen1th53/marshal/internal/store"
)

func TestA09RuntimeGateMetricsDoNotChangeDecisionSemantics(t *testing.T) {
	ctx := context.Background()
	metrics := evidence.NewMetricsRecorder()
	st, err := store.OpenWithObservability(ctx, t.TempDir()+"/policy.db", evidence.NewStrictSanitizer(evidence.SanitizerConfig{}), nil, metrics)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	p := policy.Policy{ID: "A09-RUNTIME", Version: 1, Default: policy.EffectAllow}
	digest, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutPolicy(ctx, store.PolicyRecord{Policy: p, Binding: policy.PolicyBinding{Version: 1, Digest: digest, Generation: 1}, State: policy.StateActive}); err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{store: st, policyConfigured: true, runtimePolicy: RuntimePolicyConfig{PolicyID: p.ID, PolicyVersion: p.Version}}
	if err := runtime.authorizeRuntime(ctx, "subject", "task", "Codex", "read", "repo"); err != nil {
		t.Fatal(err)
	}
	if got := metrics.Snapshot().Success[evidence.MetricOperationPolicyRuntimeGate]; got != 1 {
		t.Fatalf("runtime allow metrics = %d, want 1", got)
	}

	runtime.runtimePolicy.PolicyID = "missing"
	if err := runtime.authorizeRuntime(ctx, "subject", "task", "Codex", "read", "repo"); err == nil {
		t.Fatal("missing runtime policy unexpectedly allowed")
	}
	snapshot := metrics.Snapshot()
	if snapshot.Denied["POLICY_DENIED"] != 1 {
		t.Fatalf("runtime deny metrics = %d, want 1", snapshot.Denied["POLICY_DENIED"])
	}
}
