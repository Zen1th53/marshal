package authz

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/events"
)

// CanWithAudit records a bounded authority decision after evaluation. The
// event is a projection and never supplies authority to the caller.
func CanWithAudit(ctx context.Context, subject Principal, authority Authority, resource string, query capability.Query, broker capability.Broker, eventStore events.Store) (Decision, error) {
	decision, decisionErr := CanWithCapability(ctx, subject, authority, resource, query, broker)
	if eventStore == nil {
		return decision, decisionErr
	}
	eventType := events.Type("authz.authority.denied")
	if decision.Allowed {
		eventType = events.Type("authz.authority.allowed")
	}
	key := auditKey(string(eventType), subject.ID, string(query.TaskID), string(authority), resource, string(decision.CapabilityGrantID))
	data := map[string]string{"authority": string(authority), "reason": string(decision.Reason), "role": subject.Role.Name}
	if decision.CapabilityGrantID != "" {
		data["grant_id"] = decision.CapabilityGrantID
	}
	_, eventErr := eventStore.Append(ctx, events.Event{
		ID: events.EventID(key), Type: eventType, Subject: events.SubjectID(subject.ID),
		TaskID: events.TaskID(query.TaskID), ResourceID: events.ResourceID(resourceReference(resource)),
		IdempotencyKey: events.IdempotencyKey(key), Data: data,
	})
	if eventErr != nil {
		return decision, eventErr
	}
	return decision, decisionErr
}

func resourceReference(resource string) string {
	sum := sha256.Sum256([]byte(resource))
	return "authz-resource-" + hex.EncodeToString(sum[:])
}

func auditKey(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "authz-event-" + hex.EncodeToString(sum[:])
}
