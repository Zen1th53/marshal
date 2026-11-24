package constitution

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/proposalmcp"
)

// The protocol the Marshal reads is the one its digests pin, and every
// proposal example it shows is accepted by the live proposal schema, so the
// Marshal can copy an example and submit it on the first try.
func TestProposalSchemaProtocolDigestsMatch(t *testing.T) {
	if marshalProtocolDigest(marshalProtocol) != MarshalProtocolDigest {
		t.Fatal("base protocol text does not match MarshalProtocolDigest")
	}
	text, err := MarshalProtocol()
	if err != nil {
		t.Fatalf("shipped protocol does not match its digest: %v", err)
	}
	examples := regexp.MustCompile(`\{"action":"[^"]+"[^{}]*\}`).FindAllString(text, -1)
	if len(examples) == 0 {
		t.Fatal("protocol shows no proposal example")
	}
	for _, example := range examples {
		action := strings.SplitN(example, `"`, 5)[3]
		if !slices.Contains(proposalmcp.LiveProposalActions, action) {
			t.Errorf("protocol example names an action the live schema lacks: %s", example)
			continue
		}
		if err := proposalmcp.ValidateProposal([]byte(example)); err != nil {
			t.Errorf("protocol example %s does not match the live schema: %v", example, err)
		}
	}
}
