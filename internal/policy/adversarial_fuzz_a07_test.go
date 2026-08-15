package policy

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func FuzzPolicyDigestParse(f *testing.F) {
	f.Add("sha256:" + strings.Repeat("0", 64))
	f.Add("SHA256:" + strings.Repeat("a", 64))
	f.Fuzz(func(t *testing.T, value string) {
		err := PolicyDigest(value).Validate()
		canonical := policyDigestPattern.MatchString(value)
		if canonical && err != nil {
			t.Fatalf("canonical digest rejected: %q: %v", value, err)
		}
		if !canonical && err == nil {
			t.Fatalf("non-canonical digest accepted: %q", value)
		}
	})
}

func FuzzPolicyCanonicalization(f *testing.F) {
	f.Add("rule-a", "allow", 1)
	f.Fuzz(func(t *testing.T, ruleID, effect string, priority int) {
		p := Policy{ID: "fuzz", Version: 1, Default: EffectDeny, Rules: []Rule{{
			ID: ruleID, Description: "fuzz rule", Effect: Effect(effect), Priority: priority,
		}}}
		canonical, err := p.CanonicalJSON()
		if err != nil {
			return
		}
		parsed, err := Parse(canonical)
		if err != nil {
			t.Fatalf("canonical output did not parse: %v", err)
		}
		again, err := parsed.CanonicalJSON()
		if err != nil || string(canonical) != string(again) {
			t.Fatalf("canonical round trip changed: %q != %q", canonical, again)
		}
	})
}

func FuzzPolicyValidate(f *testing.F) {
	f.Add("subject", "read", "repo")
	f.Fuzz(func(t *testing.T, subject, action, resource string) {
		request := EvaluationRequest{SubjectID: subject, Action: Action(action), Resource: Resource(resource)}
		if err := request.Validate(); err != nil {
			return
		}
		if _, err := json.Marshal(request); err != nil {
			t.Fatalf("validated request cannot be serialized: %v", err)
		}
	})
}

func FuzzDecisionValidation(f *testing.F) {
	f.Add(true, "sha256:"+strings.Repeat("0", 64))
	f.Fuzz(func(t *testing.T, allowed bool, digest string) {
		d := Decision{Allowed: allowed, Effect: EffectAllow, PolicyDigest: PolicyDigest(digest), Binding: PolicyBinding{Version: 1, Digest: PolicyDigest(digest)}}
		if err := d.Validate(); err == nil {
			if !allowed || PolicyDigest(digest).Validate() != nil {
				t.Fatalf("invalid decision accepted: %#v", d)
			}
		}
	})
}

func FuzzEvaluationCancellation(f *testing.F) {
	f.Add("subject")
	f.Fuzz(func(t *testing.T, subject string) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		evaluator, err := NewEvaluator(Policy{ID: "fuzz", Version: 1, Default: EffectDeny})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := evaluator.Evaluate(ctx, EvaluationRequest{SubjectID: subject, Action: "read", Resource: "repo"}); err == nil {
			t.Fatal("cancelled evaluation unexpectedly succeeded")
		}
	})
}
