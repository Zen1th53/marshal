//go:build !governed

package constitution

import (
	"strings"
	"testing"
)

// Merge-order guard for the browser-only Accept: the shipped protocol must not
// ask the owner to type "accept architecture" in the chat, nor tell the
// Marshal to submit an accept-architecture proposal. The runtime no longer
// honours either; P2-01 rewrites the protocol text to match.
func TestShippedProtocolHasNoChatArchitectureAcceptance(t *testing.T) {
	text, err := MarshalProtocol()
	if err != nil {
		t.Fatal(err)
	}
	for _, chatPath := range []string{"accept-architecture", `"accept architecture"`} {
		if strings.Contains(text, chatPath) {
			t.Errorf("shipped protocol still contains the chat acceptance path %q", chatPath)
		}
	}
}
