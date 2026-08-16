package netpolicy

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type testNetworkAuthorizer struct {
	err   error
	calls int
}

func (a *testNetworkAuthorizer) AuthorizeNetwork(context.Context, Request) error {
	a.calls++
	return a.err
}

type testNetworkEvaluator struct {
	calls int
}

func (e *testNetworkEvaluator) Evaluate(_ context.Context, request Request) (Decision, error) {
	e.calls++
	return Decision{Allowed: true, RuleID: "rule-authorized", Reason: ReasonAllowed, Host: request.Host, IP: request.IP, Port: request.Port}, nil
}

func TestA04AuthorizationRunsBeforeNetworkEvaluation(t *testing.T) {
	authorizer := &testNetworkAuthorizer{}
	backend := &testNetworkEvaluator{}
	evaluator := NewAuthorizedEvaluator(backend, authorizer)
	request := Request{SubjectID: "agent-a04", TaskID: "task-a04", ChangeID: "change-a04", Host: "github.com", Protocol: ProtocolTCP, Port: 443}
	decision, err := evaluator.Evaluate(context.Background(), request)
	if err != nil || !decision.Allowed || authorizer.calls != 1 || backend.calls != 1 {
		t.Fatalf("authorized decision=%+v err=%v auth_calls=%d eval_calls=%d", decision, err, authorizer.calls, backend.calls)
	}
}

func TestA04AuthorizationDenialFailsClosedBeforeEvaluation(t *testing.T) {
	authorizer := &testNetworkAuthorizer{err: ErrDenied}
	backend := &testNetworkEvaluator{}
	evaluator := NewAuthorizedEvaluator(backend, authorizer)
	request := Request{SubjectID: "agent-a04", TaskID: "task-a04", Host: "github.com", Protocol: ProtocolTCP, Port: 443}
	decision, err := evaluator.Evaluate(context.Background(), request)
	if !errors.Is(err, ErrDenied) || decision.Allowed || decision.Reason != ReasonDenied || backend.calls != 0 {
		t.Fatalf("denied decision=%+v err=%v eval_calls=%d", decision, err, backend.calls)
	}
}

func TestA04MissingIdentityIsRejectedBeforeAuthority(t *testing.T) {
	authorizer := &testNetworkAuthorizer{}
	backend := &testNetworkEvaluator{}
	evaluator := NewAuthorizedEvaluator(backend, authorizer)
	request := Request{Host: "github.com", Protocol: ProtocolTCP, Port: 443}
	if _, err := evaluator.Evaluate(context.Background(), request); !errors.Is(err, ErrDenied) {
		t.Fatalf("missing identity error=%v, want ErrDenied", err)
	}
	if authorizer.calls != 0 || backend.calls != 0 {
		t.Fatalf("missing identity calls auth=%d eval=%d, want zero", authorizer.calls, backend.calls)
	}
}

func TestA04MissingAuthorityFailsClosed(t *testing.T) {
	backend := &testNetworkEvaluator{}
	evaluator := NewAuthorizedEvaluator(backend, nil)
	request := Request{SubjectID: "agent-a04", TaskID: "task-a04", ChangeID: "change-a04", Host: "github.com", Protocol: ProtocolTCP, Port: 443}
	decision, err := evaluator.Evaluate(context.Background(), request)
	if !errors.Is(err, ErrEnforcementUnavailable) || decision.Allowed || backend.calls != 0 {
		t.Fatalf("missing authority decision=%+v err=%v eval_calls=%d", decision, err, backend.calls)
	}
}

func TestA04AuthorityErrorDoesNotLeakSecret(t *testing.T) {
	secret := "MARSHAL_TEST_SECRET_T22_A04_AUTH"
	authorizer := &testNetworkAuthorizer{err: fmt.Errorf("backend detail: %s", secret)}
	backend := &testNetworkEvaluator{}
	evaluator := NewAuthorizedEvaluator(backend, authorizer)
	request := Request{SubjectID: "agent-a04", TaskID: "task-a04", ChangeID: "change-a04", Host: "github.com", Protocol: ProtocolTCP, Port: 443}
	decision, err := evaluator.Evaluate(context.Background(), request)
	if !errors.Is(err, ErrDenied) || decision.Allowed || backend.calls != 0 {
		t.Fatalf("authority error decision=%+v err=%v eval_calls=%d", decision, err, backend.calls)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("authority secret leaked through error: %v", err)
	}
}
