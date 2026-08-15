package policy

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const validPolicyYAML = `
id: core
version: 1
default: deny
rules:
  - id: require-review
    description: review required
    when:
      action: deploy
    effect: require
    require: [REQUIRE_APPROVAL]
    priority: 20
  - id: allow-read
    description: read allowed
    when:
      action: read
    effect: allow
    priority: 10
`

func TestPolicyContractValidatesAndEvaluates(t *testing.T) {
	evaluator, err := ParseEvaluator([]byte(validPolicyYAML))
	if err != nil {
		t.Fatal(err)
	}
	decision, err := evaluator.Evaluate(context.Background(), EvaluationRequest{SubjectID: "subject-1", Action: "read", Resource: "/repo/file"})
	if err != nil || !decision.Allowed || decision.AllowedBy != "allow-read" {
		t.Fatalf("decision=%#v err=%v", decision, err)
	}
	decision, err = evaluator.Evaluate(context.Background(), EvaluationRequest{SubjectID: "subject-1", Action: "deploy", Resource: "production"})
	if err != nil || decision.Allowed || decision.Effect != EffectRequire || len(decision.Requirements) != 1 {
		t.Fatalf("require decision=%#v err=%v", decision, err)
	}
	decision, err = evaluator.Evaluate(context.Background(), EvaluationRequest{SubjectID: "subject-1", Action: "delete", Resource: "production"})
	if err != nil || decision.Allowed || decision.Effect != EffectDeny {
		t.Fatalf("default decision=%#v err=%v", decision, err)
	}
}

func TestPolicyDenyPrecedesAllowAndRequire(t *testing.T) {
	evaluator, err := ParseEvaluator([]byte(`id: conflict
version: 1
rules:
  - id: allow
    description: allow
    when: {action: write}
    effect: allow
  - id: require
    description: require
    when: {action: write}
    effect: require
    require: [REQUIRE_APPROVAL]
  - id: deny
    description: deny
    when: {action: write}
    effect: deny
`))
	if err != nil {
		t.Fatal(err)
	}
	decision, err := evaluator.Evaluate(context.Background(), EvaluationRequest{SubjectID: "subject-1", Action: "write", Resource: "repo"})
	if err != nil || decision.Allowed || decision.Effect != EffectDeny || decision.DeniedBy != "deny" {
		t.Fatalf("decision=%#v err=%v", decision, err)
	}
}

func TestPolicyParseRejectsUnknownFieldsAndEffects(t *testing.T) {
	for name, input := range map[string]string{
		"unknown field":      "id: p\nversion: 1\nunknown: true\n",
		"unknown effect":     "id: p\nversion: 1\nrules:\n- id: r\n  description: x\n  effect: maybe\n",
		"unknown obligation": "id: p\nversion: 1\nrules:\n- id: r\n  description: x\n  effect: require\n  require: [OWNER_OVERRIDE]\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(input))
			if err == nil {
				t.Fatal("invalid policy accepted")
			}
			if ReasonCode(err) == "" {
				t.Fatalf("missing reason code: %v", err)
			}
		})
	}
}

func TestPolicyDigestCanonicalizationAndValidation(t *testing.T) {
	first, err := Parse([]byte("id: p\nversion: 1\nrules:\n- id: b\n  description: b\n  when: {z: '1', a: '2'}\n  effect: allow\n- id: a\n  description: a\n  effect: deny\n"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := Parse([]byte("version: 1\nid: p\nrules:\n- description: a\n  effect: deny\n  id: a\n- effect: allow\n  when: {a: '2', z: '1'}\n  description: b\n  id: b\n"))
	if err != nil {
		t.Fatal(err)
	}
	digest1, err := first.Digest()
	if err != nil {
		t.Fatal(err)
	}
	digest2, err := second.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if digest1 != digest2 || digest1.Validate() != nil {
		t.Fatalf("digest1=%q digest2=%q", digest1, digest2)
	}
	for _, value := range []PolicyDigest{"", "sha256:not-a-digest", PolicyDigest("SHA256:" + strings.Repeat("a", 64)), PolicyDigest("sha256:" + strings.Repeat("A", 64)), PolicyDigest("sha512:" + strings.Repeat("a", 64))} {
		if value.Validate() == nil {
			t.Fatalf("invalid digest accepted: %q", value)
		}
	}
}

func TestPolicyRequestAndDecisionDoNotAlias(t *testing.T) {
	attrs := map[string]string{"action": "read"}
	evaluator, err := ParseEvaluator([]byte(validPolicyYAML))
	if err != nil {
		t.Fatal(err)
	}
	request := EvaluationRequest{SubjectID: "subject-1", Action: "read", Resource: "repo", Attributes: attrs}
	decision, err := evaluator.Evaluate(context.Background(), request)
	if err != nil || !decision.Allowed {
		t.Fatal(err)
	}
	attrs["action"] = "deploy"
	decision.Requirements = append(decision.Requirements, ObligationApproval)
	again, err := evaluator.Evaluate(context.Background(), EvaluationRequest{SubjectID: "subject-1", Action: "read", Resource: "repo", Attributes: map[string]string{"action": "read"}})
	if err != nil || !again.Allowed || len(again.Requirements) != 0 {
		t.Fatalf("aliased decision=%#v err=%v", again, err)
	}
}

func TestPolicyErrorDoesNotExposeSecretMarker(t *testing.T) {
	marker := "MARSHAL_TEST_SECRET_T48_A01_9f3d"
	_, err := Parse([]byte("id: p\nversion: 1\nunknown: " + marker + "\n"))
	if err == nil || strings.Contains(err.Error(), marker) {
		t.Fatalf("unsafe error: %v", err)
	}
	if !errors.Is(err, ErrUnknownField) {
		t.Fatalf("unknown field reason = %q", ReasonCode(err))
	}
}

func TestPolicyProviderMetadataDoesNotImplyTrust(t *testing.T) {
	evaluator, err := ParseEvaluator([]byte("id: p\nversion: 1\ndefault: deny\nrules:\n- id: read\n  description: read\n  when: {action: read}\n  effect: allow\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"codex", "claude", "gemini", "opencode", "trusted", "owner"} {
		decision, err := evaluator.Evaluate(context.Background(), EvaluationRequest{SubjectID: "subject-1", Action: "delete", Resource: "repo", Provider: provider})
		if err != nil || decision.Allowed {
			t.Fatalf("provider %q changed denied decision: %#v err=%v", provider, decision, err)
		}
	}
}

func TestPolicyRequestRequiresTrustedIdentityFacts(t *testing.T) {
	evaluator, err := ParseEvaluator([]byte(validPolicyYAML))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := evaluator.Evaluate(context.Background(), EvaluationRequest{Action: "read", Resource: "repo"}); err == nil {
		t.Fatal("missing subject identity accepted")
	}
}

func TestPolicyBindingDistinguishesValidOldFromCurrent(t *testing.T) {
	policy, err := Parse([]byte(validPolicyYAML))
	if err != nil {
		t.Fatal(err)
	}
	digest, err := policy.Digest()
	if err != nil {
		t.Fatal(err)
	}
	old := PolicyBinding{Version: 1, Digest: digest, Generation: 1}
	if !old.FreshAgainst(PolicyBinding{Version: 1, Digest: digest, Generation: 1}) {
		t.Fatal("current binding was rejected")
	}
	if old.FreshAgainst(PolicyBinding{Version: 1, Digest: digest, Generation: 2}) {
		t.Fatal("old generation remained fresh")
	}
	if old.FreshAgainst(PolicyBinding{Version: 2, Digest: digest, Generation: 1}) {
		t.Fatal("old version remained fresh")
	}
}
