//go:build !governed

package tui

import (
	"strings"
	"testing"
)

const archGoldenName, helpGoldenName = "arch", "help"

var demoArchRules = append([]string{archTelemetryRule(true)}, archRules...)

func TestShippedClaimsMatchTheBuild(t *testing.T) {
	for _, c := range archComponents {
		if strings.Contains(strings.ToLower(c[0]), "broker") {
			t.Errorf("shipped archComponents has broker component: %q", c[0])
		}
	}
	for _, s := range businessProcess {
		if strings.Contains(s.Text, "/marshal") {
			t.Errorf("shipped businessProcess contains /marshal: step %s = %q", s.ID, s.Text)
		}
	}
	prohibited := []string{"lease", "broker", "namespace", "proxy", "bubblewrap", "process 05"}
	stringsToCheck := map[string]string{
		"securityKeyWhat":    securityKeyWhat,
		"egressStatusHeader": egressStatusHeader,
		"sandboxStatusText":  sandboxStatusText,
		"providerEgressLine": providerEgressLine,
		"modelsSafetyPolicy": modelsSafetyPolicy,
	}
	for name, val := range stringsToCheck {
		lower := strings.ToLower(val)
		for _, p := range prohibited {
			if strings.Contains(lower, p) {
				t.Errorf("%s contains prohibited word %q: %q", name, p, val)
			}
		}
	}
	if got := archRulesInForce(false)[0]; got != "Marshal: local ULTRA, telemetry off" {
		t.Errorf("archRulesInForce(false)[0] = %q, want %q", got, "Marshal: local ULTRA, telemetry off")
	}
	if got := archRulesInForce(true)[0]; got != "Marshal: local ULTRA, anonymous telemetry on" {
		t.Errorf("archRulesInForce(true)[0] = %q, want %q", got, "Marshal: local ULTRA, anonymous telemetry on")
	}
}
