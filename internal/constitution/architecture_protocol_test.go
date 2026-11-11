package constitution

import (
	"strings"
	"testing"
)

func TestArchitectureProtocolRequiresExactBrowserAcceptance(t *testing.T) {
	protocol, err := MarshalProtocol()
	if err != nil {
		t.Fatal(err)
	}
	protocol = strings.Join(strings.Fields(protocol), " ")
	for _, rule := range []string{"ARCHITECTURE REVIEW v1", "Submit to Marshal", "server-derived intersected IDs", "Reject missing, stale, forged or empty targets", "leave the architecture unchanged", "model permission grants and plan approval are not architecture acceptance", `"action":"architecture"`} {
		if !strings.Contains(protocol, rule) {
			t.Fatalf("architecture contract missing: %s", rule)
		}
	}
	if strings.Index(protocol, "ARCHITECTURE REVIEW v1") > strings.Index(protocol, "7. Plan and launch") {
		t.Fatal("architecture review occurs after task planning")
	}
	for _, rule := range []string{"architecture browser uses this same saved language", "interface uses English", "do not infer it from locale or browser settings"} {
		if !strings.Contains(protocol, rule) {
			t.Fatalf("saved interface language rule missing: %s", rule)
		}
	}
}
