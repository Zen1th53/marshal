package app

import (
	"context"
	"fmt"

	"github.com/Zen1th53/marshal/internal/evidence"
	"github.com/Zen1th53/marshal/internal/model"
)

// EvidenceTransitionRequest carries only the operation identity supplied by
// the runtime boundary. Subject and task identity are loaded from the active
// MARSHAL session rather than accepted from provider or protocol payloads.
type EvidenceTransitionRequest struct {
	SessionID   string
	NodeID      evidence.NodeID
	ChangeID    string
	TargetState evidence.State
}

func (r *Runtime) runtimeSession(ctx context.Context, sessionID string) (model.Session, error) {
	if sessionID == "" {
		return model.Session{}, fmt.Errorf("%w: evidence session is required", model.ErrConflict)
	}
	session, err := r.store.GetSession(ctx, sessionID)
	if err != nil {
		return model.Session{}, err
	}
	if session.Status != model.SessionActive || session.TaskID == nil || *session.TaskID == "" {
		return model.Session{}, fmt.Errorf("%w: evidence session is not active for a task", model.ErrConflict)
	}
	return session, nil
}

// StoreEvidence is the canonical runtime entry point for evidence creation.
// Session identity is validated before the sanitized, audited store path is
// invoked. Provider metadata is treated as untrusted node content.
func (r *Runtime) StoreEvidence(ctx context.Context, sessionID string, node evidence.Node) (evidence.Node, error) {
	if err := ctx.Err(); err != nil {
		return evidence.Node{}, err
	}
	if _, err := r.runtimeSession(ctx, sessionID); err != nil {
		return evidence.Node{}, err
	}
	return r.store.PutNode(ctx, node)
}

// Evidence reads canonical state only; no event or provider result is used as
// an authority source.
func (r *Runtime) Evidence(ctx context.Context, id evidence.NodeID) (evidence.Node, error) {
	if err := ctx.Err(); err != nil {
		return evidence.Node{}, err
	}
	return r.store.Get(ctx, id)
}

func (r *Runtime) LinkEvidence(ctx context.Context, edge evidence.Edge) (evidence.Edge, error) {
	if err := ctx.Err(); err != nil {
		return evidence.Edge{}, err
	}
	return r.store.Link(ctx, edge)
}

// TransitionEvidence binds authorization to an active runtime session and
// delegates to the A04 secured store method. Provider-supplied subject/task
// values cannot override the authenticated session identity.
func (r *Runtime) TransitionEvidence(ctx context.Context, request EvidenceTransitionRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	session, err := r.runtimeSession(ctx, request.SessionID)
	if err != nil {
		return err
	}
	return r.store.TransitionNodeAuthorized(ctx, evidence.AccessRequest{
		SubjectID:   session.AgentID,
		TaskID:      *session.TaskID,
		ChangeID:    request.ChangeID,
		NodeID:      request.NodeID,
		Action:      evidence.ActionTransition,
		TargetState: request.TargetState,
	})
}
