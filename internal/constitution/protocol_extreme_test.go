//go:build !governed

package constitution

import (
	"strings"
	"testing"
)

func TestExtremeProtocol(t *testing.T) {
	text, err := MarshalProtocol()
	if err != nil || marshalProtocolDigest(text) != ExtremeProtocolDigest || ExtremeProtocolDigest == MarshalProtocolDigest {
		t.Fatalf("digest %v", err)
	}
	for _, rule := range []string{"approval authority", "Plan approval, amendment approval, delivery and close are your decisions", "never fall back to an operator popup", "\"action\":\"decide\"", "constitutional refusals", "Ctrl+X", "/marshal stop", "reason", "hidden from chat", "Mandatory architecture-knowledge rule", "MARSHAL's MCP tools", "instead of chat memory"} {
		if !strings.Contains(text, rule) {
			t.Fatal(rule)
		}
	}
	if _, err := buildMarshalProtocol(marshalProtocol + "tampered"); err == nil {
		t.Fatal("tampering accepted")
	}
}
