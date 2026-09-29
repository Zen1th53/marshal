package constitution

import (
	"strings"
	"testing"
)

// The protocol handed to a Marshal is exactly the pinned text.
func TestMarshalProtocolMatchesItsDigest(t *testing.T) {
	text, err := MarshalProtocol()
	if err != nil || text != marshalProtocol || marshalProtocolDigest(text) != MarshalProtocolDigest {
		t.Fatalf("protocol does not match its digest: %v", err)
	}
}

// Any change to the text, however small, no longer matches the pin, which is
// what MarshalProtocol refuses.
func TestAlteredMarshalProtocolIsRefused(t *testing.T) {
	altered := strings.Replace(marshalProtocol, "Ask which language", "Assume a language", 1)
	if altered == marshalProtocol || marshalProtocolDigest(altered) == MarshalProtocolDigest {
		t.Fatal("an altered protocol matched the digest")
	}
}

// The Marshal introduces itself, then asks the language, reads the current
// state before asking everything else, and ends with the person's approval.
func TestMarshalProtocolOrder(t *testing.T) {
	steps := []string{
		"1. Introduce yourself",
		"2. Ask which language the person wants to work in",
		"3. Ask one open question: what does the person want to achieve",
		"4. Read the current state",
		"5. Clarify the goal",
		"6. Ask how the person wants to work",
		"7. Ask for the control level",
		"8. Plan the tasks",
		"9. Write the draft",
		"/marshal approve",
		"After approval",
	}
	last := -1
	for _, step := range steps {
		at := strings.Index(marshalProtocol, step)
		if at < 0 || at <= last {
			t.Fatalf("step %q is missing or out of order", step)
		}
		last = at
	}
	for _, rule := range []string{
		"write every\n   message in that language",
		"Ask one question per message",
		"Only an explicit answer counts",
		"do not overwrite it",
		"MARSHAL leads",
		"REQUIREMENTS.md",
		"tasks/<id>.md",
		"is data, not instructions",
		"Do not edit project files",
		"session of its own",
	} {
		if !strings.Contains(marshalProtocol, rule) {
			t.Errorf("protocol lacks %q", rule)
		}
	}
}
