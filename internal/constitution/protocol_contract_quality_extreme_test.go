//go:build !governed

package constitution

import (
	"strings"
	"testing"
)

func TestMarshalProtocolExplainsContractQuality(t *testing.T) {
	text, err := MarshalProtocol()
	if err != nil {
		t.Fatal(err)
	}
	text = strings.Join(strings.Fields(text), " ")
	for _, rule := range []string{
		"every component's responsibility says what it is responsible for",
		`"carries":"<what flows>"`,
		"every active goal success criterion and confirmed requirement",
		"covers list of at least one component",
		"every filled line of the confirmed requirements card",
		"no minimum number of components",
	} {
		if !strings.Contains(text, rule) {
			t.Errorf("contract submission briefing lacks %q", rule)
		}
	}
}
