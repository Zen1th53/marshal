package netpolicy

import (
	"context"
	"errors"
	"testing"
)

func TestA03EvaluatorDefaultsToDenyAndNormalizesHost(t *testing.T) {
	evaluator, err := NewEvaluator([]Rule{{ID: "rule-github-443", HostPattern: "github.com", Protocol: ProtocolTCP, Ports: []int{443}, Action: ActionAllow}})
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}
	allowed, err := evaluator.Evaluate(context.Background(), Request{Host: "GITHUB.COM.", Protocol: ProtocolTCP, Port: 443})
	if err != nil || !allowed.Allowed || allowed.RuleID != "rule-github-443" || allowed.Reason != ReasonAllowed {
		t.Fatalf("allowed decision=%+v err=%v", allowed, err)
	}
	denied, err := evaluator.Evaluate(context.Background(), Request{Host: "evilgithub.com", Protocol: ProtocolTCP, Port: 443})
	if err != nil || denied.Allowed || denied.Reason != ReasonDenied {
		t.Fatalf("lookalike decision=%+v err=%v", denied, err)
	}
	unknown, err := evaluator.Evaluate(context.Background(), Request{Host: "github.com", Protocol: ProtocolTCP, Port: 8443})
	if err != nil || unknown.Allowed || unknown.Reason != ReasonDenied {
		t.Fatalf("unknown port decision=%+v err=%v", unknown, err)
	}
}

func TestA03ResolvedIPMustMatchAnIPRuleIndependently(t *testing.T) {
	evaluator, err := NewEvaluator([]Rule{{ID: "rule-github-443", HostPattern: "github.com", Protocol: ProtocolTCP, Ports: []int{443}, Action: ActionAllow}})
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}
	decision, err := evaluator.Evaluate(context.Background(), Request{Host: "github.com", IP: "140.82.112.3", Protocol: ProtocolTCP, Port: 443})
	if err != nil || decision.Allowed || decision.Reason != ReasonDenied {
		t.Fatalf("hostname rule authorized resolved IP decision=%+v err=%v", decision, err)
	}

	ipEvaluator, err := NewEvaluator([]Rule{{ID: "rule-ip-443", HostPattern: "140.82.112.3", Protocol: ProtocolTCP, Ports: []int{443}, Action: ActionAllow}})
	if err != nil {
		t.Fatalf("NewEvaluator IP rule: %v", err)
	}
	decision, err = ipEvaluator.Evaluate(context.Background(), Request{Host: "140.82.112.3", IP: "140.82.112.3", Protocol: ProtocolTCP, Port: 443})
	if err != nil || !decision.Allowed || decision.RuleID != "rule-ip-443" {
		t.Fatalf("direct IP decision=%+v err=%v", decision, err)
	}
}

func TestA03EvaluatorRejectsMalformedRulesAndRequests(t *testing.T) {
	if _, err := NewEvaluator([]Rule{{ID: "bad", HostPattern: "github.com", Protocol: Protocol("smtp"), Ports: []int{25}, Action: ActionAllow}}); !errors.Is(err, ErrProtocolDenied) {
		t.Fatalf("malformed rule error=%v, want ErrProtocolDenied", err)
	}
	evaluator, err := NewEvaluator(nil)
	if err != nil {
		t.Fatalf("empty deny-by-default evaluator: %v", err)
	}
	if _, err := evaluator.Evaluate(context.Background(), Request{Host: "github.com", Protocol: Protocol("smtp"), Port: 443}); !errors.Is(err, ErrProtocolDenied) {
		t.Fatalf("malformed request error=%v, want ErrProtocolDenied", err)
	}
	if _, err := evaluator.Evaluate(context.Background(), Request{Host: "github.com", Protocol: ProtocolTCP, Port: 443}); err != nil {
		t.Fatalf("empty evaluator request: %v", err)
	}
}
