package netpolicy

import "testing"

func FuzzRequestValidationNeverPanicsOrAllowsMalformedProtocol(f *testing.F) {
	f.Add("github.com", "140.82.112.3", 443, string(ProtocolTCP))
	f.Add("evilgithub.com", "", 443, string(ProtocolTCP))
	f.Add("github.com", "", 443, "smtp")
	f.Add("", "", 0, "")

	f.Fuzz(func(t *testing.T, host, ip string, port int, protocol string) {
		evaluator, err := NewEvaluator([]Rule{{
			ID: "rule-github-443", HostPattern: "github.com", Protocol: ProtocolTCP,
			Ports: []int{443}, Action: ActionAllow,
		}})
		if err != nil {
			t.Fatal(err)
		}
		decision, evalErr := evaluator.Evaluate(t.Context(), Request{
			Host: host, IP: ip, Port: port, Protocol: Protocol(protocol),
		})
		if evalErr != nil && decision.Allowed {
			t.Fatalf("malformed request produced allow with error: decision=%+v err=%v", decision, evalErr)
		}
		if evalErr == nil {
			if err := decision.Validate(); err != nil {
				t.Fatalf("successful evaluation produced invalid decision: %v", err)
			}
			if decision.Allowed && decision.RuleID != "rule-github-443" {
				t.Fatalf("allow used unexpected rule: %+v", decision)
			}
		}
	})
}

func FuzzRuleValidationNeverPanics(f *testing.F) {
	f.Add("rule-github", "github.com", string(ProtocolTCP), 443, string(ActionAllow))
	f.Add("", "", "smtp", 0, "unknown")

	f.Fuzz(func(t *testing.T, id, host, protocol string, port int, action string) {
		rule := Rule{ID: RuleID(id), HostPattern: host, Protocol: Protocol(protocol), Ports: []int{port}, Action: Action(action)}
		_ = rule.Validate()
	})
}
