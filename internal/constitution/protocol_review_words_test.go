package constitution

import (
	"strings"
	"testing"
)

// TestMarshalReviewSaidInPlainWords pins that the Marshal describes its own
// fresh-context design review in plain words before the reviewer starts, so
// the owner is never asked to approve a bare command line.
func TestMarshalReviewSaidInPlainWords(t *testing.T) {
	protocol, err := MarshalProtocol()
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(strings.Fields(protocol), " ")
	for _, rule := range []string{
		"Before you start a reviewer session, tell the person in one plain sentence who reviews what and with which access",
		`"I'm asking Codex to review my design draft, read-only."`,
		"never leave them to approve a bare command line you did not describe.",
	} {
		if !strings.Contains(text, rule) {
			t.Errorf("design review guidance lacks %q", rule)
		}
	}
}
