package evidence

import (
	"errors"
	"strings"
	"testing"
)

func TestNodeValidatesCanonicalTypeAndDigest(t *testing.T) {
	node := Node{
		ID:     NodeID("EVIDENCE-001"),
		Type:   NodeTypeClaim,
		Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}

	if err := node.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	node.Type = NodeType("unknown")
	err := node.Validate()
	if !errors.Is(err, ErrInvalidType) {
		t.Fatalf("Validate() error = %v, want %v", err, ErrInvalidType)
	}
	if code := ReasonCode(err); code != CodeInvalidType {
		t.Fatalf("ReasonCode() = %q, want %q", code, CodeInvalidType)
	}
}

func TestNewErrorKeepsCauseWithoutLeakingSecret(t *testing.T) {
	const secret = "MARSHAL_TEST_SECRET_A01"
	cause := errors.New(secret)
	err := NewError(CodeSecretRejected, cause)

	if !errors.Is(err, cause) {
		t.Fatalf("NewError() does not preserve cause")
	}
	if got := err.Error(); strings.Contains(got, secret) {
		t.Fatalf("NewError() leaked secret: %q", got)
	}
}

func TestEdgeRejectsInvalidGraphBoundary(t *testing.T) {
	for _, edge := range []Edge{
		{},
		{From: "EVIDENCE-001", To: "EVIDENCE-001", Relation: "derived-from"},
		{From: "EVIDENCE-001", To: "EVIDENCE-002"},
	} {
		if err := edge.Validate(); !errors.Is(err, ErrInvalidEdge) {
			t.Fatalf("Validate(%+v) error = %v, want %v", edge, err, ErrInvalidEdge)
		}
	}
}

func TestCloneNodeDoesNotAliasMetadata(t *testing.T) {
	original := Node{Metadata: map[string]string{"source": "test"}}
	clone := CloneNode(original)
	clone.Metadata["source"] = "changed"

	if original.Metadata["source"] != "test" {
		t.Fatalf("CloneNode() changed original metadata: %#v", original.Metadata)
	}
}

func TestCanonicalDigestIsIndependentOfMetadataInsertionOrder(t *testing.T) {
	first, err := CanonicalDigest(NodeTypeClaim, map[string]string{"a": "1", "b": "2"})
	if err != nil {
		t.Fatalf("CanonicalDigest() error = %v", err)
	}
	second, err := CanonicalDigest(NodeTypeClaim, map[string]string{"b": "2", "a": "1"})
	if err != nil {
		t.Fatalf("CanonicalDigest() error = %v", err)
	}
	if first != second {
		t.Fatalf("CanonicalDigest() = %q and %q for equivalent metadata", first, second)
	}
}
