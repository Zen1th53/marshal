package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/evidence"
	"github.com/Zen1th53/marshal/internal/model"
)

type runtimeEvidenceAuthorizer struct{ allow bool }

func (a runtimeEvidenceAuthorizer) Authorize(_ context.Context, req evidence.AccessRequest) (evidence.AuthorizationDecision, error) {
	if !a.allow {
		return evidence.AuthorizationDecision{Allowed: false}, nil
	}
	return evidence.AuthorizationDecision{
		Allowed: true, SubjectID: req.SubjectID, TaskID: req.TaskID, ChangeID: req.ChangeID,
		NodeID: req.NodeID, State: req.CurrentState, PolicyDigest: "sha256:runtime-policy",
		FreshUntil: time.Now().UTC().Add(time.Minute),
	}, nil
}

func TestRuntimeEvidenceMutationUsesSecuredAuditedBoundary(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	rt, err := OpenWithOptions(context.Background(), repo.Path(), Options{EvidenceAuthorizer: runtimeEvidenceAuthorizer{allow: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	agent, err := rt.RegisterAgent(context.Background(), RegisterAgentRequest{Name: "runtime-evidence", Role: "developer"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.ImportTasks(context.Background(), []model.Task{{ID: "TASK-A06", Title: "evidence", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	claim, err := rt.Claim(context.Background(), ClaimRequest{TaskID: "TASK-A06", AgentID: agent.ID, ExpectedRevision: 0})
	if err != nil {
		t.Fatal(err)
	}
	digest, err := evidence.CanonicalDigest(evidence.NodeTypeClaim, map[string]string{"value": "runtime"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.StoreEvidence(context.Background(), claim.Session.ID, evidence.Node{ID: "NODE-A06", Type: evidence.NodeTypeClaim, Digest: digest, Metadata: map[string]string{"value": "runtime"}}); err != nil {
		t.Fatal(err)
	}
	if err := rt.TransitionEvidence(context.Background(), EvidenceTransitionRequest{SessionID: claim.Session.ID, NodeID: "NODE-A06", ChangeID: "CHANGE-A06", TargetState: evidence.StateLinked}); err != nil {
		t.Fatal(err)
	}
	node, err := rt.Evidence(context.Background(), "NODE-A06")
	if err != nil {
		t.Fatal(err)
	}
	if node.State != evidence.StateLinked {
		t.Fatalf("state = %s", node.State)
	}
}

func TestRuntimeEvidenceDeniedDoesNotMutate(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	rt, err := OpenWithOptions(context.Background(), repo.Path(), Options{EvidenceAuthorizer: runtimeEvidenceAuthorizer{allow: false}})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	agent, err := rt.RegisterAgent(context.Background(), RegisterAgentRequest{Name: "runtime-deny", Role: "developer"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.ImportTasks(context.Background(), []model.Task{{ID: "TASK-A06-DENY", Title: "evidence", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	claim, err := rt.Claim(context.Background(), ClaimRequest{TaskID: "TASK-A06-DENY", AgentID: agent.ID, ExpectedRevision: 0})
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := evidence.CanonicalDigest(evidence.NodeTypeClaim, map[string]string{"value": "deny"})
	if _, err := rt.StoreEvidence(context.Background(), claim.Session.ID, evidence.Node{ID: "NODE-A06-DENY", Type: evidence.NodeTypeClaim, Digest: digest, Metadata: map[string]string{"value": "deny"}}); err != nil {
		t.Fatal(err)
	}
	err = rt.TransitionEvidence(context.Background(), EvidenceTransitionRequest{SessionID: claim.Session.ID, NodeID: "NODE-A06-DENY", ChangeID: "CHANGE-A06", TargetState: evidence.StateLinked})
	if !errors.Is(err, evidence.ErrAuthorizationDenied) {
		t.Fatalf("err = %v", err)
	}
	node, err := rt.Evidence(context.Background(), "NODE-A06-DENY")
	if err != nil {
		t.Fatal(err)
	}
	if node.State != evidence.StateStored {
		t.Fatalf("state = %s", node.State)
	}
}

func TestRuntimeEvidenceCancellationDoesNotMutate(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	rt, err := OpenWithOptions(context.Background(), repo.Path(), Options{EvidenceAuthorizer: runtimeEvidenceAuthorizer{allow: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	agent, err := rt.RegisterAgent(context.Background(), RegisterAgentRequest{Name: "runtime-cancel", Role: model.RoleDeveloper})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.ImportTasks(context.Background(), []model.Task{{ID: "TASK-A06-CANCEL", Title: "evidence", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	claim, err := rt.Claim(context.Background(), ClaimRequest{TaskID: "TASK-A06-CANCEL", AgentID: agent.ID, ExpectedRevision: 0})
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := evidence.CanonicalDigest(evidence.NodeTypeClaim, map[string]string{"value": "cancel"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = rt.StoreEvidence(ctx, claim.Session.ID, evidence.Node{ID: "NODE-A06-CANCEL", Type: evidence.NodeTypeClaim, Digest: digest, Metadata: map[string]string{"value": "cancel"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if _, err := rt.Evidence(context.Background(), "NODE-A06-CANCEL"); evidence.ReasonCode(err) != evidence.CodeInvalidEdge {
		t.Fatalf("cancelled operation persisted evidence: %v", err)
	}
}

func TestRuntimeEvidenceRequiresAuthorizer(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	rt, err := Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	agent, err := rt.RegisterAgent(context.Background(), RegisterAgentRequest{Name: "runtime-no-auth", Role: model.RoleDeveloper})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.ImportTasks(context.Background(), []model.Task{{ID: "TASK-A06-NOAUTH", Title: "evidence", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	claim, err := rt.Claim(context.Background(), ClaimRequest{TaskID: "TASK-A06-NOAUTH", AgentID: agent.ID, ExpectedRevision: 0})
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := evidence.CanonicalDigest(evidence.NodeTypeClaim, map[string]string{"value": "no-auth"})
	if _, err := rt.StoreEvidence(context.Background(), claim.Session.ID, evidence.Node{ID: "NODE-A06-NOAUTH", Type: evidence.NodeTypeClaim, Digest: digest, Metadata: map[string]string{"value": "no-auth"}}); err != nil {
		t.Fatal(err)
	}
	err = rt.TransitionEvidence(context.Background(), EvidenceTransitionRequest{SessionID: claim.Session.ID, NodeID: "NODE-A06-NOAUTH", ChangeID: "CHANGE-A06", TargetState: evidence.StateLinked})
	if !errors.Is(err, evidence.ErrAuthorizationUnavailable) {
		t.Fatalf("err = %v", err)
	}
	node, err := rt.Evidence(context.Background(), "NODE-A06-NOAUTH")
	if err != nil {
		t.Fatal(err)
	}
	if node.State != evidence.StateStored {
		t.Fatalf("state = %s", node.State)
	}
}
