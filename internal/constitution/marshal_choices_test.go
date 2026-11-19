package constitution

import (
	"strings"
	"testing"
)

func TestMarshalChatChoicesUseWords(t *testing.T) {
	protocol, err := MarshalProtocol()
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(strings.Fields(protocol), " ")
	for _, rule := range []string{
		"choice words followed by Enter",
		`Reply "build on it" (recommended), "leave it alone", or "wait", then press Enter.`,
		"every provider chat needs a non-empty answer",
		"The Board sends the chosen choice's words, including for its Enter key.",
	} {
		if !strings.Contains(text, rule) {
			t.Errorf("choice guidance lacks %q", rule)
		}
	}
	if strings.Contains(text, "[Enter] build on it") {
		t.Fatal("chat still advertises an empty submit")
	}
}
