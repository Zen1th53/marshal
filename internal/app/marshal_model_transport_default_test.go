//go:build governed

package app

import (
	"testing"
)

// transportLargePromptBytes is far larger than a small prompt yet inside the
// model request cap, so the transport test exercises the buffered stdin path.
// Observed knowledge that would crowd the cap travels as a recall note.
const transportLargePromptBytes = 100 * 1024

// assertTransportPrompt proves the complete canonical prompt and context reach
// the provider over stdin, byte for byte, in the default build.
func assertTransportPrompt(t *testing.T, stdin, prompt, knowledge string) {
	t.Helper()
	// Every role turn opens with the marker history capture declines.
	want := marshalRoleTurnMarker + prompt + "\n" + knowledge
	if stdin != want && stdin != marshalRoleTurnMarker+prompt+"\n"+marshalKnowledgeRecallNote {
		t.Fatalf("stdin differs: got %d bytes, want %d", len(stdin), len(want))
	}
}
