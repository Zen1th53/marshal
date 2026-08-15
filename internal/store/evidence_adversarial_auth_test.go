package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/evidence"
)

func TestA07RejectsMalformedPolicyDigestBeforeStateMutation(t *testing.T) {
	node := testEvidenceNode("EVIDENCE-A07-POLICY", "claim", "policy")
	authorizer := fixedAuthorizer{decision: evidence.AuthorizationDecision{
		Allowed: true, SubjectID: "subject-a07", NodeID: node.ID,
		State: evidence.StateStored, PolicyDigest: "sha256:not-a-digest",
		FreshUntil: time.Now().UTC().Add(time.Minute),
	}}
	st := openEvidenceStoreWithSecurity(t, evidence.NewStrictSanitizer(evidence.SanitizerConfig{}), authorizer)
	if _, err := st.PutNode(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	err := st.TransitionNodeAuthorized(context.Background(), evidence.AccessRequest{
		SubjectID: "subject-a07", NodeID: node.ID, Action: evidence.ActionTransition,
		TargetState: evidence.StateLinked,
	})
	if !errors.Is(err, evidence.ErrAuthorizationStale) {
		t.Fatalf("error = %v, want stale authorization", err)
	}
	got, err := st.Get(context.Background(), node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != evidence.StateStored {
		t.Fatalf("state = %q, want stored", got.State)
	}
}

func TestA07PolicyDigestRequiresCanonicalLowercaseSHA256(t *testing.T) {
	for _, digest := range []string{
		"", "sha256:", "sha256:" + strings.Repeat("a", 63),
		"sha256:" + strings.Repeat("a", 65), "SHA256:" + strings.Repeat("a", 64),
		"sha256:" + strings.Repeat("A", 64),
	} {
		if evidence.ValidDigest(digest) {
			t.Fatalf("digest unexpectedly accepted: %q", digest)
		}
	}
	if !evidence.ValidDigest("sha256:" + strings.Repeat("a", 64)) {
		t.Fatal("canonical digest rejected")
	}
}
