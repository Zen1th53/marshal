//go:build governed

package app

import (
	"testing"
)

// transportLargePromptBytes is larger than any single argv string may be on a
// typical host, so the transport test proves the prompt never travels in argv.
const transportLargePromptBytes = 512 * 1024

// assertTransportPrompt proves the complete canonical prompt and context reach
// the provider over stdin, byte for byte, in the default build.
func assertTransportPrompt(t *testing.T, stdin, prompt, knowledge string) {
	t.Helper()
	// Every role turn opens with the marker history capture declines.
	want := marshalRoleTurnMarker + prompt + "\n" + knowledge
	if stdin != want {
		t.Fatalf("stdin differs: got %d bytes, want %d", len(stdin), len(want))
	}
}
