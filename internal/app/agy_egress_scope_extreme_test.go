//go:build !governed && linux

package app

import (
	"errors"
	"reflect"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/netpolicy"
)

func TestAGYExplicitRequestRequiresExactlyBothNativeEndpoints(t *testing.T) {
	rule := func(host string) netpolicy.Rule {
		return netpolicy.Rule{ID: "fixture-rule", HostPattern: host, Ports: []int{443}, Protocol: netpolicy.ProtocolTCP, Action: netpolicy.ActionAllow}
	}
	daily, www := rule("daily-cloudcode-pa.googleapis.com"), rule("www.googleapis.com")
	for _, tc := range []struct {
		name  string
		rules []netpolicy.Rule
		pass  bool
	}{
		{"default", nil, true}, {"both", []netpolicy.Rule{daily, www}, true}, {"primary-only", []netpolicy.Rule{daily}, false}, {"secondary-only", []netpolicy.Rule{www}, false}, {"duplicate", []netpolicy.Rule{daily, www, www}, false}, {"auth-host", []netpolicy.Rule{daily, rule("accounts.google.com")}, false}, {"wrong-port", []netpolicy.Rule{daily, {ID: "fixture-wrong-port", HostPattern: "www.googleapis.com", Ports: []int{80}, Protocol: netpolicy.ProtocolTCP, Action: netpolicy.ActionAllow}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := validateProviderRequestEgress("agy", "gemini-3.8-flash-high", tc.rules)
			if tc.pass && e != nil || !tc.pass && !errors.Is(e, model.ErrPolicyDenied) {
				t.Fatalf("explicit native scope pass=%v err=%v", tc.pass, e)
			}
		})
	}
	endpoints, e := providerEgressEndpoints("agy", "gemini-3.8-flash-high")
	if e != nil || !reflect.DeepEqual(endpoints, []string{"daily-cloudcode-pa.googleapis.com:443", "www.googleapis.com:443"}) {
		t.Fatal("provider default scope is not the exact native pair")
	}
	endpoints[1] = "accounts.google.com:443"
	again, _ := providerEgressEndpoints("agy", "gemini-3.8-flash-high")
	if again[1] != "www.googleapis.com:443" {
		t.Fatal("provider default endpoint backing was mutable")
	}
	if e := validateProviderRequestEgress("claude", "sonnet", []netpolicy.Rule{rule("api.anthropic.com")}); e != nil {
		t.Fatal("unrelated provider request changed")
	}
}
