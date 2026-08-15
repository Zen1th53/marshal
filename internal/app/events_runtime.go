package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/model"
)

type runtimeEventIdentityContextKey struct{}

type runtimeEventIdentityProvider struct{}

func (runtimeEventIdentityProvider) Identity(ctx context.Context) (events.ProducerIdentity, error) {
	identity, ok := ctx.Value(runtimeEventIdentityContextKey{}).(events.ProducerIdentity)
	if !ok || identity.SubjectID == "" || identity.SessionID == "" {
		return events.ProducerIdentity{}, events.ErrAuthorizationUnavailable
	}
	return identity, nil
}

func withRuntimeEventIdentity(ctx context.Context, identity events.ProducerIdentity) context.Context {
	return context.WithValue(ctx, runtimeEventIdentityContextKey{}, identity)
}

func (r *Runtime) reconcileClaim(ctx context.Context, request ClaimRequest) (ClaimResult, bool, error) {
	task, err := r.store.GetTask(ctx, request.TaskID)
	if err != nil {
		if errorsIsNotFound(err) {
			return ClaimResult{}, false, nil
		}
		return ClaimResult{}, false, err
	}
	if task.Status != model.TaskClaimed || task.Revision != request.ExpectedRevision+1 || task.OwnerAgentID == nil || *task.OwnerAgentID != request.AgentID {
		return ClaimResult{}, false, nil
	}
	active, err := r.store.ActiveLease(ctx, request.TaskID)
	if err != nil {
		return ClaimResult{}, false, nil
	}
	if active.AgentID != request.AgentID || active.TaskRevision != request.ExpectedRevision+1 {
		return ClaimResult{}, false, nil
	}
	session, err := r.store.GetSession(ctx, active.Lease.SessionID)
	if err != nil {
		return ClaimResult{}, false, err
	}
	if session.AgentID != request.AgentID || session.TaskID == nil || *session.TaskID != request.TaskID || session.Status != model.SessionActive {
		return ClaimResult{}, false, nil
	}
	return ClaimResult{Lease: active.Lease, Session: session}, true, nil
}

func errorsIsNotFound(err error) bool {
	// The store wraps model.ErrNotFound with context; callers must use typed
	// error identity rather than parsing the human message.
	return errors.Is(err, model.ErrNotFound)
}

func (r *Runtime) recordLeaseAcquired(ctx context.Context, claim ClaimResult) error {
	if r.eventStream == nil {
		return nil
	}
	identity := events.ProducerIdentity{
		SubjectID: events.SubjectID(claim.Session.AgentID),
		SessionID: claim.Session.ID,
		TaskID:    events.TaskID(claim.Lease.TaskID),
		ChangeID:  "LEASE-ACQUIRE-" + claim.Lease.ID,
	}
	event := events.Event{
		ID:         events.EventID("EVENT-LEASE-ACQUIRED-" + claim.Lease.ID),
		Type:       events.Type("scheduler.lease.acquired"),
		Subject:    identity.SubjectID,
		TaskID:     identity.TaskID,
		ResourceID: events.ResourceID(claim.Lease.ID),
		Data: map[string]string{
			"result":         "acquired",
			"session_id":     claim.Session.ID,
			"lease_revision": strconv.FormatInt(claim.Lease.Revision, 10),
		},
		IdempotencyKey: events.IdempotencyKey("RUNTIME-LEASE-ACQUIRED-" + claim.Lease.ID),
	}
	result, err := r.eventStream.Process(withRuntimeEventIdentity(ctx, identity), event)
	if err != nil {
		if events.ReasonCode(err) != "" {
			return err
		}
		return events.NewError(events.CodeStoreFailed, err)
	}
	if result.State != events.StatePublished {
		return events.NewError(events.CodeStoreFailed, fmt.Errorf("structured event did not reach published state"))
	}
	return nil
}

func (r *Runtime) StructuredEventsSince(ctx context.Context, after events.Sequence, limit int) ([]events.Event, error) {
	if r.eventStream == nil {
		return nil, events.ErrAuthorizationUnavailable
	}
	return r.eventStream.Since(ctx, after, limit)
}

func (r *Runtime) SubscribeStructuredEvents(ctx context.Context, after events.Sequence) (<-chan events.Event, func(), error) {
	if r.eventStream == nil {
		return nil, nil, events.ErrAuthorizationUnavailable
	}
	return r.eventStream.Subscribe(ctx, after)
}
