package netpolicy

import (
	"errors"
	"testing"
)

func TestA01RuleRequestAndDecisionContract(t *testing.T) {
	rule := Rule{
		ID: "rule-github-443", HostPattern: "github.com", Protocol: ProtocolTCP,
		Ports: []int{443}, Action: ActionAllow,
	}
	if err := rule.Validate(); err != nil {
		t.Fatalf("valid rule: %v", err)
	}

	request := Request{Host: "github.com", IP: "140.82.112.3", Protocol: ProtocolTCP, Port: 443}
	if err := request.Validate(); err != nil {
		t.Fatalf("valid request: %v", err)
	}

	decision := Decision{Allowed: true, RuleID: rule.ID, Reason: ReasonAllowed, Host: request.Host, IP: request.IP, Port: request.Port}
	if err := decision.Validate(); err != nil {
		t.Fatalf("valid decision: %v", err)
	}
}

func TestA01UnknownProtocolAndMalformedRuleAreTypedErrors(t *testing.T) {
	if err := (Rule{ID: "rule-invalid", HostPattern: "github.com", Protocol: Protocol("smtp"), Ports: []int{25}, Action: ActionAllow}).Validate(); !errors.Is(err, ErrProtocolDenied) {
		t.Fatalf("unknown protocol error=%v, want ErrProtocolDenied", err)
	}
	if err := (Rule{ID: "rule-invalid", HostPattern: "evil github.com", Protocol: ProtocolTCP, Ports: []int{443}, Action: ActionAllow}).Validate(); !errors.Is(err, ErrRuleInvalid) {
		t.Fatalf("malformed host error=%v, want ErrRuleInvalid", err)
	}
	if err := (Rule{ID: "rule-invalid", HostPattern: "github.com", Protocol: ProtocolTCP, Ports: []int{0}, Action: ActionAllow}).Validate(); !errors.Is(err, ErrRuleInvalid) {
		t.Fatalf("invalid port error=%v, want ErrRuleInvalid", err)
	}
}

func TestA01DecisionValidationIsClosedAndErrorsAreSafe(t *testing.T) {
	if err := (Decision{Allowed: true, RuleID: "", Reason: ReasonAllowed, Host: "github.com", Port: 443}).Validate(); !errors.Is(err, ErrRuleInvalid) {
		t.Fatalf("unbound allow decision error=%v, want ErrRuleInvalid", err)
	}
	if err := (Decision{Allowed: false, Reason: Reason("NET_UNKNOWN"), Host: "github.com", Port: 443}).Validate(); !errors.Is(err, ErrRuleInvalid) {
		t.Fatalf("unknown decision reason=%v, want ErrRuleInvalid", err)
	}
	marker := "MARSHAL_TEST_SECRET_T22_A01"
	if err := (Rule{ID: "rule-marker", HostPattern: marker, Protocol: ProtocolTCP, Ports: []int{443}, Action: ActionAllow}).Validate(); err == nil || err.Error() == marker {
		t.Fatalf("marker-bearing rule error=%v, want stable redacted error", err)
	}
}

func TestIPv6HostPatternValidation(t *testing.T) {
	validIPv6s := []string{
		"2001:db8::1",
		"[2001:db8::1]",
		"::1",
		"[::1]",
		"fe80::1",
		"[fe80::1]",
		"::",
		"[::]",
		"::ffff:192.0.2.1",
		"[::ffff:192.0.2.1]",
	}

	for _, v := range validIPv6s {
		r := Rule{ID: "rule-ipv6", HostPattern: v, Protocol: ProtocolTCP, Ports: []int{443}, Action: ActionAllow}
		if err := r.Validate(); err != nil {
			t.Errorf("expected valid IPv6 rule for %q, got error: %v", v, err)
		}

		req := Request{Host: v, Protocol: ProtocolTCP, Port: 443}
		if err := req.Validate(); err != nil {
			t.Errorf("expected valid IPv6 request for %q, got error: %v", v, err)
		}
	}

	invalidIPv6s := []string{
		"[2001:db8::1",
		"2001:db8::1]",
		"[2001:db8::1]trailing",
		"prefix[2001:db8::1]",
		"2001:db8:::1",
		"[2001:db8:::1]",
		"2001:db8:xyz::1",
	}

	for _, v := range invalidIPv6s {
		r := Rule{ID: "rule-bad-ipv6", HostPattern: v, Protocol: ProtocolTCP, Ports: []int{443}, Action: ActionAllow}
		if err := r.Validate(); err == nil {
			t.Errorf("expected invalid IPv6 rule for %q, got nil error", v)
		}
	}
}
