package dag

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type a04Backend struct{ putNodeCalls int }

func (b *a04Backend) PutDAGNode(context.Context, Node) (Node, error) {
	b.putNodeCalls++
	return Node{TaskID: "TASK-A", Kind: NodeKindTask, Status: StatusPending}, nil
}
func (*a04Backend) GetDAGNode(context.Context, TaskID) (Node, error)     { return Node{}, ErrNodeNotFound }
func (*a04Backend) PutDAGEdge(context.Context, Edge) (Edge, error)       { return Edge{}, nil }
func (*a04Backend) DAGEdgesFrom(context.Context, TaskID) ([]Edge, error) { return nil, nil }
func (*a04Backend) DAGEdgesTo(context.Context, TaskID) ([]Edge, error)   { return nil, nil }
func (*a04Backend) DAGNodes(context.Context) ([]Node, error)             { return nil, nil }
func (*a04Backend) TransitionDAGNode(context.Context, TaskID, NodeStatus, NodeStatus) (Node, error) {
	return Node{}, nil
}

func TestT29A04MutationFailsClosedWithoutAuthority(t *testing.T) {
	backend := &a04Backend{}
	engine, err := NewEngine(backend)
	if err != nil {
		t.Fatal(err)
	}

	_, err = engine.AddNode(context.Background(), AddNodeRequest{
		RequestID: "REQ-A04-NODE",
		Node:      Node{TaskID: "TASK-A", Kind: NodeKindTask, Status: StatusPending},
	})
	if !errors.Is(err, ErrAuthorizationUnavailable) {
		t.Fatalf("AddNode error = %v, want authorization unavailable", err)
	}
	if backend.putNodeCalls != 0 {
		t.Fatalf("PutDAGNode calls = %d, want 0", backend.putNodeCalls)
	}
}

type a04IdentityProvider struct {
	identity Identity
	err      error
}

func (p a04IdentityProvider) Identity(context.Context) (Identity, error) { return p.identity, p.err }

type a04Authorizer struct {
	decide func(AuthorizationRequest) AuthorizationDecision
	err    error
}

func (a a04Authorizer) Authorize(_ context.Context, r AuthorizationRequest) (AuthorizationDecision, error) {
	if a.err != nil {
		return AuthorizationDecision{}, a.err
	}
	return a.decide(r), nil
}

func a04Identity() Identity {
	return Identity{SubjectID: "SUBJECT-A04", SessionID: "SESSION-A04", TaskID: "TASK-CALLER", ChangeID: "CHANGE-A04"}
}

func a04Allow(r AuthorizationRequest) AuthorizationDecision {
	return AuthorizationDecision{
		Allowed: true, Identity: r.Identity, RequestID: r.RequestID, Action: r.Action,
		Resource: r.Resource, ExpectedState: r.ExpectedState, TargetState: r.TargetState,
		PolicyDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		FreshUntil:   time.Now().Add(time.Hour),
	}
}

func TestT29A04AuthorizationBindsExactResourceAndFreshness(t *testing.T) {
	request := AddNodeRequest{RequestID: "REQ-A04-AUTH", Node: Node{TaskID: "TASK-A", Kind: NodeKindTask, Status: StatusPending}}
	tests := []struct {
		name   string
		decide func(AuthorizationRequest) AuthorizationDecision
		want   error
	}{
		{name: "wrong-resource", decide: func(r AuthorizationRequest) AuthorizationDecision {
			d := a04Allow(r)
			d.Resource = nodeResource("TASK-B")
			return d
		}, want: ErrAuthorizationDenied},
		{name: "expired", decide: func(r AuthorizationRequest) AuthorizationDecision {
			d := a04Allow(r)
			d.FreshUntil = time.Now().Add(-time.Second)
			return d
		}, want: ErrAuthorizationStale},
		{name: "malformed-policy-digest", decide: func(r AuthorizationRequest) AuthorizationDecision {
			d := a04Allow(r)
			d.PolicyDigest = "not-a-digest"
			return d
		}, want: ErrAuthorizationStale},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := &a04Backend{}
			engine, err := NewAuthorizedEngine(backend, a04IdentityProvider{identity: a04Identity()}, a04Authorizer{decide: tt.decide}, FreshnessValidatorFunc(func(context.Context, AuthorizationRequest, AuthorizationDecision) error { return nil }))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := engine.AddNode(context.Background(), request); !errors.Is(err, tt.want) {
				t.Fatalf("AddNode error=%v, want %v", err, tt.want)
			}
			if backend.putNodeCalls != 0 {
				t.Fatalf("PutDAGNode calls=%d, want 0", backend.putNodeCalls)
			}
		})
	}
}

func TestT29A04ValidAuthorizationMutatesExactlyOnce(t *testing.T) {
	backend := &a04Backend{}
	engine, err := NewAuditedEngine(backend, a04IdentityProvider{identity: a04Identity()}, a04Authorizer{decide: a04Allow}, FreshnessValidatorFunc(func(context.Context, AuthorizationRequest, AuthorizationDecision) error { return nil }), &a05EventSink{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.AddNode(context.Background(), AddNodeRequest{RequestID: "REQ-A04-ALLOW", Node: Node{TaskID: "TASK-A", Kind: NodeKindTask, Status: StatusPending}}); err != nil {
		t.Fatalf("AddNode: %v", err)
	}
	if backend.putNodeCalls != 1 {
		t.Fatalf("PutDAGNode calls=%d, want 1", backend.putNodeCalls)
	}
}

func TestT29A04AuthorityBackendErrorDoesNotLeakSecret(t *testing.T) {
	const marker = "MARSHAL_TEST_SECRET_T29_A04_7c91"
	backend := &a04Backend{}
	engine, err := NewAuthorizedEngine(backend, a04IdentityProvider{identity: a04Identity()}, a04Authorizer{err: errors.New(marker)}, FreshnessValidatorFunc(func(context.Context, AuthorizationRequest, AuthorizationDecision) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.AddNode(context.Background(), AddNodeRequest{RequestID: "REQ-A04-SECRET", Node: Node{TaskID: "TASK-A", Kind: NodeKindTask, Status: StatusPending}})
	if !errors.Is(err, ErrAuthorizationUnavailable) {
		t.Fatalf("error=%v", err)
	}
	if strings.Contains(err.Error(), marker) {
		t.Fatalf("public error leaked marker: %q", err)
	}
	if backend.putNodeCalls != 0 {
		t.Fatalf("PutDAGNode calls=%d, want 0", backend.putNodeCalls)
	}
}

func TestT29A04CanonicalFreshnessFailurePreventsMutation(t *testing.T) {
	const marker = "MARSHAL_TEST_SECRET_T29_A04_FRESH_28bd"
	backend := &a04Backend{}
	engine, err := NewAuthorizedEngine(
		backend,
		a04IdentityProvider{identity: a04Identity()},
		a04Authorizer{decide: a04Allow},
		FreshnessValidatorFunc(func(context.Context, AuthorizationRequest, AuthorizationDecision) error { return errors.New(marker) }),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.AddNode(context.Background(), AddNodeRequest{RequestID: "REQ-A04-FRESH", Node: Node{TaskID: "TASK-A", Kind: NodeKindTask, Status: StatusPending}})
	if !errors.Is(err, ErrAuthorizationStale) {
		t.Fatalf("error=%v, want stale", err)
	}
	if strings.Contains(err.Error(), marker) {
		t.Fatalf("public error leaked freshness marker: %q", err)
	}
	if backend.putNodeCalls != 0 {
		t.Fatalf("PutDAGNode calls=%d, want 0", backend.putNodeCalls)
	}
}
