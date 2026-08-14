package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/evidence"
)

type fixedAuthorizer struct {
	decision evidence.AuthorizationDecision
	err      error
}

func (a fixedAuthorizer) Authorize(context.Context, evidence.AccessRequest) (evidence.AuthorizationDecision, error) {
	return a.decision, a.err
}

func openEvidenceStoreWithSecurity(t *testing.T, sanitizer evidence.Sanitizer, authorizer evidence.Authorizer) *Store {
	t.Helper()
	st, err := OpenWithSecurity(context.Background(), t.TempDir()+"/state.db", sanitizer, authorizer)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestDeniedEvidenceTransitionDoesNotMutateCanonicalState(t *testing.T) {
	st := openEvidenceStoreWithSecurity(t, evidence.NewStrictSanitizer(evidence.SanitizerConfig{}), fixedAuthorizer{decision: evidence.AuthorizationDecision{Allowed: false, ReasonCode: evidence.CodeAuthorizationDenied}})
	node := testEvidenceNode("EVIDENCE-A04-DENY", "claim", "deny")
	if _, err := st.PutNode(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	err := st.TransitionNodeAuthorized(context.Background(), evidence.AccessRequest{SubjectID: "subject-1", NodeID: node.ID, Action: evidence.ActionTransition, TargetState: evidence.StateLinked})
	if !errors.Is(err, evidence.ErrAuthorizationDenied) {
		t.Fatalf("error = %v, want %v", err, evidence.ErrAuthorizationDenied)
	}
	got, err := st.Get(context.Background(), node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != evidence.StateStored {
		t.Fatalf("state = %q, want stored", got.State)
	}
}

func TestAllowedEvidenceTransitionRequiresFreshBoundDecision(t *testing.T) {
	node := testEvidenceNode("EVIDENCE-A04-ALLOW", "claim", "allow")
	base := evidence.AuthorizationDecision{Allowed: true, SubjectID: "subject-1", NodeID: node.ID, State: evidence.StateStored, PolicyDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", FreshUntil: time.Now().UTC().Add(time.Minute)}
	st := openEvidenceStoreWithSecurity(t, evidence.NewStrictSanitizer(evidence.SanitizerConfig{}), fixedAuthorizer{decision: base})
	if _, err := st.PutNode(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	if err := st.TransitionNodeAuthorized(context.Background(), evidence.AccessRequest{SubjectID: "foreign", NodeID: node.ID, Action: evidence.ActionTransition, TargetState: evidence.StateLinked}); !errors.Is(err, evidence.ErrAuthorizationDenied) {
		t.Fatalf("foreign subject error = %v", err)
	}
	if err := st.TransitionNodeAuthorized(context.Background(), evidence.AccessRequest{SubjectID: "subject-1", NodeID: node.ID, Action: evidence.ActionTransition, TargetState: evidence.StateLinked}); err != nil {
		t.Fatalf("allowed transition: %v", err)
	}
}

func TestEvidenceAuthorizationFailsClosedForMissingExpiredAndBackendError(t *testing.T) {
	node := testEvidenceNode("EVIDENCE-A04-FAILCLOSED", "claim", "failclosed")
	base := evidence.AuthorizationDecision{Allowed: true, SubjectID: "subject-1", NodeID: node.ID, State: evidence.StateStored, PolicyDigest: "sha256:" + strings.Repeat("a", 64), FreshUntil: time.Now().UTC().Add(time.Minute)}
	for name, tc := range map[string]struct {
		authorizer evidence.Authorizer
		want       error
	}{
		"missing": {nil, evidence.ErrAuthorizationUnavailable},
		"expired": {fixedAuthorizer{decision: func() evidence.AuthorizationDecision {
			d := base
			d.FreshUntil = time.Now().UTC().Add(-time.Minute)
			return d
		}()}, evidence.ErrAuthorizationStale},
		"backend-error": {fixedAuthorizer{err: errors.New("MARSHAL_TEST_SECRET_T06_A04_backend")}, evidence.ErrAuthorizationUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			st := openEvidenceStoreWithSecurity(t, evidence.NewStrictSanitizer(evidence.SanitizerConfig{}), tc.authorizer)
			if _, err := st.PutNode(context.Background(), node); err != nil {
				t.Fatal(err)
			}
			err := st.TransitionNodeAuthorized(context.Background(), evidence.AccessRequest{SubjectID: "subject-1", NodeID: node.ID, Action: evidence.ActionTransition, TargetState: evidence.StateLinked})
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if strings.Contains(err.Error(), "MARSHAL_TEST_SECRET") {
				t.Fatalf("secret leaked in error: %v", err)
			}
			got, err := st.Get(context.Background(), node.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.State != evidence.StateStored {
				t.Fatalf("state = %q, want stored", got.State)
			}
		})
	}
}

func TestForeignTaskAndChangeAuthorizationAreDenied(t *testing.T) {
	node := testEvidenceNode("EVIDENCE-A04-SCOPE", "claim", "scope")
	decision := evidence.AuthorizationDecision{Allowed: true, SubjectID: "subject-1", TaskID: "task-1", ChangeID: "change-1", NodeID: node.ID, State: evidence.StateStored, PolicyDigest: "sha256:" + strings.Repeat("b", 64), FreshUntil: time.Now().UTC().Add(time.Minute)}
	st := openEvidenceStoreWithSecurity(t, evidence.NewStrictSanitizer(evidence.SanitizerConfig{}), fixedAuthorizer{decision: decision})
	if _, err := st.PutNode(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	for _, request := range []evidence.AccessRequest{
		{SubjectID: "subject-1", TaskID: "task-foreign", ChangeID: "change-1", NodeID: node.ID, Action: evidence.ActionTransition, TargetState: evidence.StateLinked},
		{SubjectID: "subject-1", TaskID: "task-1", ChangeID: "change-foreign", NodeID: node.ID, Action: evidence.ActionTransition, TargetState: evidence.StateLinked},
	} {
		if err := st.TransitionNodeAuthorized(context.Background(), request); !errors.Is(err, evidence.ErrAuthorizationDenied) {
			t.Fatalf("foreign scope error = %v", err)
		}
	}
}
