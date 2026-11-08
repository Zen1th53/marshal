//go:build !governed

package constitution

import (
	"strings"
	"testing"
)

func TestExtremeDeliveredProtocolUsesPrivateStateAndExactDigest(t *testing.T) {
	text, err := MarshalProtocol()
	if err != nil {
		t.Fatal("protocol rendering failed")
	}
	if strings.Contains(text, ".marshal-extreme/") || !strings.Contains(text, ".marshal/proposals/") {
		t.Fatal("delivered protocol points at another state namespace")
	}
	if marshalProtocolDigest(text) != ExtremeProtocolDigest {
		t.Fatal("delivered protocol binding is not its exact digest")
	}
	if marshalProtocolDigest(marshalProtocol) != MarshalProtocolDigest {
		t.Fatal("original source protocol was changed")
	}
	if _, err := buildMarshalProtocol(strings.ReplaceAll(marshalProtocol, ".marshal/", ".marshal-extreme/../.marshal/")); err == nil {
		t.Fatal("unexpected namespace expansion accepted")
	}
}
