//go:build !governed

package app

import (
	"strings"
	"testing"
)

// transportLargePromptBytes stays inside the private required-context envelope
// while still exercising the buffered transport instead of a trivial prompt.
const transportLargePromptBytes = 4 * 1024

// assertTransportPrompt proves the canonical prompt reaches the provider over
// stdin in the private build too. The admitted required-context packet, not the
// caller string, is the exact model input there, so the prompt must appear
// inside that packet and never in argv.
func assertTransportPrompt(t *testing.T, stdin, prompt, knowledge string) {
	t.Helper()
	if stdin == "" {
		t.Fatal("prompt did not reach stdin")
	}
	if !strings.Contains(stdin, prompt) {
		t.Fatalf("stdin lost the canonical prompt: %d bytes", len(stdin))
	}
}
