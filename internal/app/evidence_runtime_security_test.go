package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/evidence"
)

type capturingRuntimeAuthorizer struct {
	seen evidence.AccessRequest
}

func (a *capturingRuntimeAuthorizer) Authorize(_ context.Context, request evidence.AccessRequest) (evidence.AuthorizationDecision, error) {
	a.seen = request
	return evidence.AuthorizationDecision{
		Allowed: true, SubjectID: request.SubjectID, TaskID: request.TaskID,
		ChangeID: request.ChangeID, NodeID: request.NodeID, State: request.CurrentState,
		PolicyDigest: "sha256:" + strings.Repeat("b", 64),
		FreshUntil:   time.Now().UTC().Add(time.Minute),
	}, nil
}

func TestRuntimeEvidenceBindsAuthorizationToTrustedContext(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	authorizer := &capturingRuntimeAuthorizer{}
	rt, err := OpenWithOptions(context.Background(), repo.Path(), Options{EvidenceAuthorizer: authorizer})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	node := a06SecurityNode("EVIDENCE-A06-FORGED")
	if _, err := rt.PutEvidence(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	trusted := WithEvidenceIdentity(context.Background(), EvidenceIdentity{
		SubjectID: "trusted-subject", TaskID: "trusted-task", ChangeID: "trusted-change",
	})
	if err := rt.TransitionEvidence(trusted, EvidenceTransitionRequest{NodeID: node.ID, TargetState: evidence.StateLinked}); err != nil {
		t.Fatal(err)
	}
	if authorizer.seen.SubjectID != "trusted-subject" || authorizer.seen.TaskID != "trusted-task" || authorizer.seen.ChangeID != "trusted-change" {
		t.Fatalf("authorization used untrusted identity: %#v", authorizer.seen)
	}
}

func TestRuntimeEvidenceCancellationBeforeAuthorizationDoesNotMutate(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	authorizer := &capturingRuntimeAuthorizer{}
	rt, err := OpenWithOptions(context.Background(), repo.Path(), Options{EvidenceAuthorizer: authorizer})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	node := a06SecurityNode("EVIDENCE-A06-CANCEL")
	if _, err := rt.PutEvidence(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(WithEvidenceIdentity(context.Background(), EvidenceIdentity{
		SubjectID: "subject", TaskID: "task", ChangeID: "change",
	}))
	cancel()
	err = rt.TransitionEvidence(ctx, EvidenceTransitionRequest{NodeID: node.ID, TargetState: evidence.StateLinked})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled transition error = %v", err)
	}
	got, err := rt.Evidence(context.Background(), node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != evidence.StateStored {
		t.Fatalf("cancelled transition state = %s", got.State)
	}
	if authorizer.seen.NodeID != "" {
		t.Fatal("authorizer was called after cancellation")
	}
}

type secretRuntimeAuthorizer struct{}

func (secretRuntimeAuthorizer) Authorize(context.Context, evidence.AccessRequest) (evidence.AuthorizationDecision, error) {
	return evidence.AuthorizationDecision{}, fmt.Errorf("backend detail: %s", "MARSHAL_TEST_SECRET_T06_A06_runtime")
}

func TestRuntimeEvidenceAuthorizationErrorDoesNotLeakSecret(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	rt, err := OpenWithOptions(context.Background(), repo.Path(), Options{EvidenceAuthorizer: secretRuntimeAuthorizer{}})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	node := a06SecurityNode("EVIDENCE-A06-SECRET")
	if _, err := rt.PutEvidence(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	ctx := WithEvidenceIdentity(context.Background(), EvidenceIdentity{SubjectID: "subject", TaskID: "task", ChangeID: "change"})
	err = rt.TransitionEvidence(ctx, EvidenceTransitionRequest{NodeID: node.ID, TargetState: evidence.StateLinked})
	if err == nil || strings.Contains(err.Error(), "MARSHAL_TEST_SECRET_T06_A06_runtime") {
		t.Fatalf("secret leaked through runtime error: %v", err)
	}
	got, err := rt.Evidence(context.Background(), node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != evidence.StateStored {
		t.Fatalf("failed transition state = %s", got.State)
	}
	events, err := rt.Events(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if strings.Contains(fmt.Sprint(event.Data), "MARSHAL_TEST_SECRET_T06_A06_runtime") {
			t.Fatal("secret marker persisted in audit event")
		}
	}
}

func TestRuntimeEvidenceRetryDoesNotDuplicateAuditSuccess(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	rt, err := OpenWithOptions(context.Background(), repo.Path(), Options{EvidenceAuthorizer: a06AllowAuthorizer{}})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	node := a06SecurityNode("EVIDENCE-A06-RETRY")
	if _, err := rt.PutEvidence(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	ctx := WithEvidenceIdentity(context.Background(), EvidenceIdentity{SubjectID: "subject", TaskID: "task", ChangeID: "change"})
	request := EvidenceTransitionRequest{NodeID: node.ID, TargetState: evidence.StateLinked}
	if err := rt.TransitionEvidence(ctx, request); err != nil {
		t.Fatal(err)
	}
	if err := rt.TransitionEvidence(ctx, request); err != nil {
		t.Fatal(err)
	}
	events, err := rt.Events(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var transitions int
	for _, event := range events {
		if event.Type == "evidence.state.transitioned" {
			transitions++
		}
	}
	if transitions != 1 {
		t.Fatalf("transition audit facts = %d, want 1", transitions)
	}
}

type a06AllowAuthorizer struct{}

func (a06AllowAuthorizer) Authorize(_ context.Context, request evidence.AccessRequest) (evidence.AuthorizationDecision, error) {
	return evidence.AuthorizationDecision{
		Allowed: true, SubjectID: request.SubjectID, TaskID: request.TaskID,
		ChangeID: request.ChangeID, NodeID: request.NodeID, State: request.CurrentState,
		PolicyDigest: "sha256:" + strings.Repeat("c", 64), FreshUntil: time.Now().UTC().Add(time.Minute),
	}, nil
}

func a06SecurityNode(id string) evidence.Node {
	metadata := map[string]string{"source": "a06-security"}
	digest, err := evidence.CanonicalDigest(evidence.NodeTypeOutput, metadata)
	if err != nil {
		panic(err)
	}
	return evidence.Node{ID: evidence.NodeID(id), Type: evidence.NodeTypeOutput, Digest: digest, CreatedAt: time.Now().UTC(), Metadata: metadata}
}
