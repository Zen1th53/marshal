package constitution

import (
	"strings"
	"testing"
)

func TestMarshalProtocolKeepsOneProjectArchitecture(t *testing.T) {
	protocol, err := MarshalProtocol()
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range []string{"next revision of the one project contract", "removed:true", "whole project's top-level", "connection's carries", "Tie each confirmed requirement", "press a on the Board", "supersedes_digest"} {
		if !strings.Contains(protocol, rule) {
			t.Errorf("protocol lacks %q", rule)
		}
	}
}
