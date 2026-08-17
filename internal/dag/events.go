package dag

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/Zen1th53/marshal/internal/events"
)

// EventSink is the narrow T43 boundary consumed by T29. The canonical
// events.Engine satisfies it; tests may use deterministic fakes.
type EventSink interface {
	Append(context.Context, events.Event) (events.Event, error)
}

type EventSinkFunc func(context.Context, events.Event) (events.Event, error)

func (f EventSinkFunc) Append(ctx context.Context, event events.Event) (events.Event, error) {
	return f(ctx, event)
}

func (e *Engine) emitMutationEvent(ctx context.Context, typ string, decision AuthorizationDecision, requestID RequestID, resource MutationResource, result string, reason Code, targetState string, condition string) error {
	if e.eventSink == nil {
		return ErrEventUnavailable
	}
	resourceID := mutationResourceID(resource)
	canonical := strings.Join([]string{typ, decision.Identity.SubjectID, decision.Identity.SessionID, decision.Identity.TaskID, decision.Identity.ChangeID, string(requestID), string(decision.Action), resourceID, string(decision.ExpectedState), string(decision.TargetState), decision.PolicyDigest, result, string(reason), targetState, condition}, "\x00")
	sum := sha256.Sum256([]byte(canonical))
	hexsum := hex.EncodeToString(sum[:])
	data := map[string]string{
		"action":        string(decision.Action),
		"change_id":     decision.Identity.ChangeID,
		"policy_digest": decision.PolicyDigest,
		"result":        result,
	}
	if requestID != "" {
		data["request_id"] = string(requestID)
	}
	if reason != "" {
		data["reason_code"] = string(reason)
	}
	if targetState != "" {
		data["target_state"] = targetState
	}
	if condition != "" {
		data["condition"] = condition
	}
	event := events.Event{
		ID: events.EventID("EVENT-DAG-" + hexsum), Type: events.Type(typ), Subject: events.SubjectID(decision.Identity.SubjectID),
		TaskID: events.TaskID(decision.Identity.TaskID), ResourceID: events.ResourceID(resourceID), Data: data,
		IdempotencyKey: events.IdempotencyKey("DAG-EVENT-" + hexsum),
	}
	if err := event.Validate(); err != nil {
		return NewError(CodeEventUnavailable, err)
	}
	if _, err := e.eventSink.Append(ctx, event); err != nil {
		return NewError(CodeEventUnavailable, err)
	}
	return nil
}

func mutationResourceID(resource MutationResource) string {
	if resource.Node != "" {
		return string(resource.Node)
	}
	sum := sha256.Sum256([]byte(string(resource.Parent) + "\x00" + string(resource.Child)))
	return "DAG-EDGE-" + hex.EncodeToString(sum[:])
}
