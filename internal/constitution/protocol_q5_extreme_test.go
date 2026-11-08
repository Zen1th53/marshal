//go:build !governed

package constitution

import (
	"strings"
	"testing"
)

func TestExtremeProtocolOperationalChoicesAreMarshals(t *testing.T) {
	text, err := MarshalProtocol()
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range []string{"Operational choices are yours", "option A\nor B", "reassignment", "decided_by=marshal", "never ask the owner", "Only architecture acceptance, publishing and force-push or deletions stay with\nthe owner"} {
		if !strings.Contains(text, rule) {
			t.Fatal(rule)
		}
	}
}
