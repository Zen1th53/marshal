package store

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/evidence"
)

type a08PolicyGenerationAuthorizer struct {
	entered chan struct{}
	release chan struct{}
	mu      sync.RWMutex
	current string
}

func (a *a08PolicyGenerationAuthorizer) Authorize(_ context.Context, req evidence.AccessRequest) (evidence.AuthorizationDecision, error) {
	close(a.entered)
	<-a.release
	return evidence.AuthorizationDecision{
		Allowed: true, SubjectID: req.SubjectID, TaskID: req.TaskID, ChangeID: req.ChangeID,
		NodeID: req.NodeID, State: req.CurrentState, PolicyDigest: "sha256:" + strings.Repeat("1", 64),
		FreshUntil: time.Now().UTC().Add(time.Minute),
	}, nil
}

func (a *a08PolicyGenerationAuthorizer) ValidateFreshness(_ context.Context, _ evidence.AccessRequest, decision evidence.AuthorizationDecision) error {
	a.mu.RLock()
	current := a.current
	a.mu.RUnlock()
	if decision.PolicyDigest != current {
		return evidence.ErrAuthorizationStale
	}
	return nil
}

func TestA08ValidPolicyDecisionBecomesStaleBeforeMutation(t *testing.T) {
	ctx := context.Background()
	a := &a08PolicyGenerationAuthorizer{
		entered: make(chan struct{}), release: make(chan struct{}),
		current: "sha256:" + strings.Repeat("1", 64),
	}
	st := openEvidenceStoreWithSecurity(t, evidence.NewStrictSanitizer(evidence.SanitizerConfig{}), a)
	node := testEvidenceNode("EVIDENCE-A08-POLICY-RACE", "claim", "policy")
	if _, err := st.PutNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		result <- st.TransitionNodeAuthorized(ctx, evidence.AccessRequest{
			SubjectID: "subject", TaskID: "task", ChangeID: "change", NodeID: node.ID,
			Action: evidence.ActionTransition, TargetState: evidence.StateLinked,
		})
	}()
	<-a.entered
	a.mu.Lock()
	a.current = "sha256:" + strings.Repeat("2", 64)
	a.mu.Unlock()
	close(a.release)
	if err := <-result; !errors.Is(err, evidence.ErrAuthorizationStale) {
		t.Fatalf("stale P1 decision error = %v, want %v", err, evidence.ErrAuthorizationStale)
	}
	got, err := st.Get(ctx, node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != evidence.StateStored {
		t.Fatalf("canonical state = %q, want stored", got.State)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM audit_events WHERE event_type = 'evidence.state.transitioned'"); got != 0 {
		t.Fatalf("semantic transition audits = %d, want 0", got)
	}
}
