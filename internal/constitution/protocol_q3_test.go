package constitution

import (
	"strings"
	"testing"
)

func TestProtocolNoPollingAndSingleReturnPackage(t *testing.T) {
	text, err := MarshalProtocol()
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range []string{"Do not poll in a loop inside one turn", "run_digest", "since the last next_cursor", "then end the turn", "Return a task as one package", "file:line", "Never return one finding at a time"} {
		if !strings.Contains(text, rule) {
			t.Fatal(rule)
		}
	}
}
