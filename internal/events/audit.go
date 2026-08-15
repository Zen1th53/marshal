package events

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
)

func (e *Engine) recordAppended(ctx context.Context, stored Event, decision AuthorizationDecision) (Event, error) {
	canonical := strings.Join([]string{"events.appended", string(stored.ID), string(stored.IdempotencyKey), strconv.FormatUint(uint64(stored.Sequence), 10), decision.PolicyDigest}, "\x00")
	sum := sha256.Sum256([]byte(canonical))
	digest := hex.EncodeToString(sum[:])
	audit := Event{
		ID:         EventID("EVENT-STREAM-AUDIT-" + digest),
		Type:       Type("events.appended"),
		Subject:    SubjectID(decision.Identity.SubjectID),
		TaskID:     TaskID(decision.Identity.TaskID),
		RunID:      RunID(decision.Identity.RunID),
		ResourceID: ResourceID(stored.ID),
		Data: map[string]string{
			"change_id":     decision.Identity.ChangeID,
			"event_type":    string(stored.Type),
			"policy_digest": decision.PolicyDigest,
			"result":        "appended",
			"sequence":      strconv.FormatUint(uint64(stored.Sequence), 10),
		},
		IdempotencyKey: IdempotencyKey("EVENT-STREAM-AUDIT-" + digest),
	}
	appended, err := e.store.Append(ctx, audit)
	if err != nil {
		if ReasonCode(err) != "" {
			return Event{}, err
		}
		return Event{}, NewError(CodeStoreFailed, err)
	}
	return CloneEvent(appended), nil
}

func (e *Engine) recordSchemaRejected(ctx context.Context, rejected Event, cause error) error {
	if e.identity == nil || e.authorizer == nil || e.freshness == nil {
		return ErrAuthorizationUnavailable
	}
	identity, err := e.identity.Identity(ctx)
	if err != nil {
		return NewError(CodeAuthorizationUnavailable, err)
	}
	if !identity.valid() {
		return ErrAuthorizationDenied
	}
	raw, err := json.Marshal(rejected)
	if err != nil {
		return NewError(CodeInvalidEvent, err)
	}
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	reason := ReasonCode(cause)
	if reason == "" {
		reason = CodeInvalidEvent
	}
	audit := Event{
		ID: EventID("EVENT-SCHEMA-REJECT-" + digest), Type: Type("events.schema.rejected"),
		Subject: SubjectID(identity.SubjectID), TaskID: TaskID(identity.TaskID), RunID: RunID(identity.RunID),
		ResourceID:     ResourceID("schema-" + digest[:32]),
		Data:           map[string]string{"change_id": identity.ChangeID, "result": "rejected", "reason_code": string(reason)},
		IdempotencyKey: IdempotencyKey("EVENT-SCHEMA-REJECT-" + digest),
	}
	if _, err := e.authorize(ctx, audit); err != nil {
		return err
	}
	stored, err := e.store.Append(ctx, audit)
	if err != nil {
		if ReasonCode(err) != "" {
			return err
		}
		return NewError(CodeStoreFailed, err)
	}
	if err := e.bus.Publish(ctx, stored); err != nil {
		return NewError(CodeStoreFailed, err)
	}
	return nil
}

func (e *Engine) recordSubscriberDropped(ctx context.Context, source Event) error {
	canonical := strings.Join([]string{"events.subscriber.dropped", string(source.ID), strconv.FormatUint(uint64(source.Sequence), 10)}, "\x00")
	sum := sha256.Sum256([]byte(canonical))
	digest := hex.EncodeToString(sum[:])
	audit := Event{
		ID: EventID("EVENT-SUBSCRIBER-DROP-" + digest), Type: Type("events.subscriber.dropped"),
		Subject: source.Subject, TaskID: source.TaskID, RunID: source.RunID, ResourceID: ResourceID(source.ID),
		Data:           map[string]string{"event_type": string(source.Type), "result": "dropped", "reason_code": "subscriber_buffer_full", "sequence": strconv.FormatUint(uint64(source.Sequence), 10)},
		IdempotencyKey: IdempotencyKey("EVENT-SUBSCRIBER-DROP-" + digest),
	}
	if _, err := e.store.Append(ctx, audit); err != nil {
		if ReasonCode(err) != "" {
			return err
		}
		return NewError(CodeStoreFailed, err)
	}
	return nil
}
