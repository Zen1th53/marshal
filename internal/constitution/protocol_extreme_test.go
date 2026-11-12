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
	for _, rule := range []string{"approval authority", "Plan approval, amendment approval, delivery and close are your decisions", "never fall back to an operator popup", "\"action\":\"decide\"", "constitutional refusals", "Ctrl+X", "browser", "reason", "hidden from chat", "Mandatory architecture-knowledge rule", "MARSHAL's MCP tools", "instead of chat memory"} {
		if !strings.Contains(text, rule) {
			t.Fatal(rule)
		}
	}
	if _, err := buildMarshalProtocol(marshalProtocol + "tampered"); err == nil {
		t.Fatal("tampering accepted")
	}
}

// The shipped edition supersedes operator popups, so the Marshal it briefs
// must receive none of the base protocol's stale popup/press-A instructions.
func TestExtremeProtocolHasNoStalePopupInstructions(t *testing.T) {
	text, err := MarshalProtocol()
	if err != nil {
		t.Fatal(err)
	}
	flat := strings.Join(strings.Fields(text), " ")
	// The authority variant keeps negations such as "without an operator
	// popup"; only the base protocol's affirmative popup instructions are stale.
	for _, stale := range []string{
		"MARSHAL will show a popup; press A to apply",
		"amend proposal and operator popup",
		"close proposals with operator popups",
		"its approval popup",
		"You cannot approve it yourself",
		"F7/F8/F9/F11/F12 navigate",
	} {
		if strings.Contains(flat, stale) {
			t.Errorf("shipped protocol still has stale popup instruction %q", stale)
		}
	}
	// The real controls the Marshal may tell the person about.
	for _, want := range []string{"stop all workers at any time with Ctrl+X", "pause or resume the run from the action palette"} {
		if !strings.Contains(flat, want) {
			t.Errorf("shipped protocol lacks the real control wording %q", want)
		}
	}
}
