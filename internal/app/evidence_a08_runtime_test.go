package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/evidence"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

// terminatingEvidenceAuthorizer models a session becoming invalid after the
// runtime has resolved it but before the secured store mutation begins.
type terminatingEvidenceAuthorizer struct {
	db      *store.Store
	session model.Session
}

func (a terminatingEvidenceAuthorizer) Authorize(_ context.Context, req evidence.AccessRequest) (evidence.AuthorizationDecision, error) {
	if err := a.db.TerminateSession(context.Background(), a.session.ID, model.SessionTerminated, a.session.Revision); err != nil {
		return evidence.AuthorizationDecision{}, err
	}
	return evidence.AuthorizationDecision{
		Allowed: true, SubjectID: req.SubjectID, TaskID: req.TaskID, ChangeID: req.ChangeID,
		NodeID: req.NodeID, State: req.CurrentState,
		PolicyDigest: "sha256:" + strings.Repeat("a", 64),
		FreshUntil:   time.Now().UTC().Add(time.Minute),
	}, nil
}

func TestRuntimeRejectsSessionInvalidatedDuringAuthorization(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	authorizer := &terminatingEvidenceAuthorizer{}
	rt, err := OpenWithOptions(context.Background(), repo.Path(), Options{EvidenceAuthorizer: authorizer})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	agent, err := rt.RegisterAgent(context.Background(), RegisterAgentRequest{Name: "a08-session-race", Role: model.RoleDeveloper})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.ImportTasks(context.Background(), []model.Task{{ID: "TASK-A08-SESSION-RACE", Title: "evidence", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	claim, err := rt.Claim(context.Background(), ClaimRequest{TaskID: "TASK-A08-SESSION-RACE", AgentID: agent.ID, ExpectedRevision: 0})
	if err != nil {
		t.Fatal(err)
	}
	node := evidence.Node{ID: "NODE-A08-SESSION-RACE", Type: evidence.NodeTypeClaim, Metadata: map[string]string{"value": "race"}}
	node.Digest, err = evidence.CanonicalDigest(node.Type, node.Metadata)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.StoreEvidence(context.Background(), claim.Session.ID, node); err != nil {
		t.Fatal(err)
	}
	authorizer.db = rt.store
	authorizer.session = claim.Session
	err = rt.TransitionEvidence(context.Background(), EvidenceTransitionRequest{
		SessionID: claim.Session.ID, NodeID: node.ID, ChangeID: "CHANGE-A08", TargetState: evidence.StateLinked,
	})
	if !errors.Is(err, evidence.ErrAuthorizationStale) && !errors.Is(err, evidence.ErrAuthorizationDenied) {
		t.Fatalf("invalidated session transition error = %v", err)
	}
	got, err := rt.Evidence(context.Background(), node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != evidence.StateStored {
		t.Fatalf("session invalidation mutated state to %q", got.State)
	}
}
