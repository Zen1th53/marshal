package netpolicy

import (
	"context"
	"strings"
)

// Authorizer is the authenticated authority boundary for network operations.
// It is deliberately separate from Evaluator so policy matching cannot grant
// permission by itself.
type Authorizer interface {
	AuthorizeNetwork(context.Context, Request) error
}

// AuthorizedEvaluator checks the authenticated authority boundary before it
// invokes the pure network-policy evaluator.
type AuthorizedEvaluator struct {
	evaluator  Evaluator
	authorizer Authorizer
}

func NewAuthorizedEvaluator(evaluator Evaluator, authorizer Authorizer) *AuthorizedEvaluator {
	return &AuthorizedEvaluator{evaluator: evaluator, authorizer: authorizer}
}

func (e *AuthorizedEvaluator) Evaluate(ctx context.Context, request Request) (Decision, error) {
	if err := request.Validate(); err != nil {
		return deniedDecision(request, reasonForError(err)), err
	}
	if strings.TrimSpace(request.SubjectID) == "" ||
		strings.TrimSpace(request.TaskID) == "" ||
		strings.TrimSpace(request.ChangeID) == "" {
		return deniedDecision(request, ReasonDenied), ErrDenied
	}
	if e == nil || e.evaluator == nil || e.authorizer == nil {
		return deniedDecision(request, ReasonEnforcementUnavailable), ErrEnforcementUnavailable
	}
	if err := e.authorizer.AuthorizeNetwork(ctx, request); err != nil {
		return deniedDecision(request, ReasonDenied), ErrDenied
	}
	return e.evaluator.Evaluate(ctx, request)
}

func deniedDecision(request Request, reason Reason) Decision {
	return Decision{Host: request.Host, IP: request.IP, Port: request.Port, Reason: reason}
}

var _ Evaluator = (*AuthorizedEvaluator)(nil)
