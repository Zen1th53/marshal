package constitution

import (
	"strings"
	"testing"
)

// The architecture page opens only when the person presses y on the Board,
// so no edition's protocol has the Marshal say it is already open.
func TestMarshalProtocolSaysArchitectureOpensOnY(t *testing.T) {
	text, err := MarshalProtocol()
	if err != nil {
		t.Fatal(err)
	}
	flat := strings.Join(strings.Fields(text), " ")
	if strings.Contains(flat, "The architecture is open in your browser") {
		t.Error("the protocol has the Marshal claim the architecture is already open")
	}
	if !strings.Contains(flat, "The architecture is ready. Press y on the Board to open it in your browser") {
		t.Error("the protocol does not say the page opens when the person presses y")
	}
}
