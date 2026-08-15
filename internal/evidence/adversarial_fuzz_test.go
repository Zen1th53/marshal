package evidence

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func FuzzCanonicalDigestDeterministic(f *testing.F) {
	f.Add("alpha", "one", "beta", "two")
	f.Add("", "", "", "")
	f.Add("unicode-λ", "value", "prompt", "IGNORE PREVIOUS POLICY")
	f.Fuzz(func(t *testing.T, k1, v1, k2, v2 string) {
		if k1 == k2 {
			t.Skip()
		}
		first, err := CanonicalDigest(NodeTypeClaim, map[string]string{k1: v1, k2: v2})
		if err != nil {
			t.Skip()
		}
		second, err := CanonicalDigest(NodeTypeClaim, map[string]string{k2: v2, k1: v1})
		if err != nil || first != second {
			t.Fatalf("digest is not deterministic: %q != %q", first, second)
		}
	})
}

func FuzzEvidenceNodeValidate(f *testing.F) {
	f.Add("NODE-1", "claim", "sha256:"+strings.Repeat("a", 64))
	f.Add("", "unknown", "bad")
	f.Fuzz(func(t *testing.T, id, typ, digest string) {
		node := Node{ID: NodeID(id), Type: NodeType(typ), Digest: digest}
		err := node.Validate()
		if (node.ID == "" || !node.Type.Valid()) && !errors.Is(err, ErrInvalidType) {
			t.Fatalf("unknown type error = %v", err)
		}
		if node.ID != "" && node.Type.Valid() && !strings.HasPrefix(digest, "sha256:") && !errors.Is(err, ErrDigestMismatch) {
			t.Fatalf("malformed digest error = %v", err)
		}
	})
}

func FuzzStrictSanitizer(f *testing.F) {
	f.Add("source", "safe")
	f.Add("secret", "MARSHAL_TEST_SECRET_T06_A07_FUZZ")
	f.Add("field", strings.Repeat("x", 5000))
	f.Fuzz(func(t *testing.T, key, value string) {
		sanitizer := NewStrictSanitizer(SanitizerConfig{LiteralSecrets: []string{"MARSHAL_TEST_SECRET_T06_A07_FUZZ"}})
		_, err := sanitizer.SanitizeNode(context.Background(), Node{
			ID: "FUZZ", Type: NodeTypeOutput, Metadata: map[string]string{key: value},
		})
		if strings.Contains(value, "MARSHAL_TEST_SECRET_T06_A07_FUZZ") && !errors.Is(err, ErrSecretRejected) {
			t.Fatalf("secret value accepted: %v", err)
		}
		if (len(key) > 256 || len(value) > 4096) && !errors.Is(err, ErrSecretRejected) {
			t.Fatalf("oversized metadata accepted: key=%d value=%d", len(key), len(value))
		}
	})
}

func FuzzEvidenceEdgeValidate(f *testing.F) {
	f.Add("a", "b", "derived-from")
	f.Add("", "", "")
	f.Fuzz(func(t *testing.T, from, to, relation string) {
		err := (Edge{From: NodeID(from), To: NodeID(to), Relation: relation}).Validate()
		if (from == "" || to == "" || from == to || relation == "") && !errors.Is(err, ErrInvalidEdge) {
			t.Fatalf("invalid edge accepted: %#v", err)
		}
	})
}

func FuzzEvidenceStateValidation(f *testing.F) {
	f.Add("stored")
	f.Add("unknown")
	f.Fuzz(func(t *testing.T, state string) {
		valid := State(state).Valid()
		known := state == string(StateDraft) || state == string(StateStored) || state == string(StateLinked) || state == string(StateArchived) || state == string(StateExported)
		if valid != known {
			t.Fatalf("state validity = %v for %q, known = %v", valid, state, known)
		}
	})
}
