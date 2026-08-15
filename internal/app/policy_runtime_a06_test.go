package app

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/policy"
)

func TestRuntimeDeniedByPolicyDoesNotExecuteOperation(t *testing.T) {
	p := policy.Policy{ID: "runtime", Version: 1, Default: policy.EffectDeny}
	evaluator, err := policy.NewEvaluator(p)
	if err != nil {
		t.Fatal(err)
	}
	binding := policy.PolicyBinding{Version: 1, Digest: mustPolicyDigest(t, p), Generation: 1}
	calls := 0
	err = executeWithPolicy(context.Background(), evaluator, binding, policy.EvaluationRequest{
		SubjectID: "agent-1", Action: "shell.execute", Resource: "repo",
	}, func() error { calls++; return nil })
	if err == nil || calls != 0 {
		t.Fatalf("err=%v calls=%d, want deny and zero calls", err, calls)
	}
}

func TestRuntimePolicyRequiresActiveAllowAndRejectsRequirements(t *testing.T) {
	p := policy.Policy{ID: "runtime", Version: 1, Default: policy.EffectDeny,
		Rules: []policy.Rule{{ID: "approval", Description: "approval required", When: map[string]string{"action": "shell.execute"}, Effect: policy.EffectRequire, Require: []policy.Obligation{policy.ObligationApproval}}}}
	evaluator, err := policy.NewEvaluator(p)
	if err != nil {
		t.Fatal(err)
	}
	binding := policy.PolicyBinding{Version: 1, Digest: mustPolicyDigest(t, p), Generation: 2}
	calls := 0
	err = executeWithPolicy(context.Background(), evaluator, binding, policy.EvaluationRequest{
		SubjectID: "agent-1", Action: "shell.execute", Resource: "repo", Provider: "Codex",
	}, func() error { calls++; return nil })
	if err == nil || calls != 0 {
		t.Fatalf("requirement err=%v calls=%d", err, calls)
	}
}

func TestRuntimePolicyProviderLabelsDoNotGrantAuthority(t *testing.T) {
	p := policy.Policy{ID: "runtime", Version: 1, Default: policy.EffectDeny}
	evaluator, err := policy.NewEvaluator(p)
	if err != nil {
		t.Fatal(err)
	}
	binding := policy.PolicyBinding{Version: 1, Digest: mustPolicyDigest(t, p), Generation: 1}
	for _, provider := range []string{"Codex", "Claude", "Gemini", "OpenCode", "system", "root"} {
		calls := 0
		err = executeWithPolicy(context.Background(), evaluator, binding, policy.EvaluationRequest{
			SubjectID: "agent-1", Action: "shell.execute", Resource: "repo", Provider: provider,
		}, func() error { calls++; return nil })
		if err == nil || calls != 0 {
			t.Fatalf("provider %q err=%v calls=%d", provider, err, calls)
		}
	}
}

func mustPolicyDigest(t *testing.T, p policy.Policy) policy.PolicyDigest {
	t.Helper()
	d, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return d
}
