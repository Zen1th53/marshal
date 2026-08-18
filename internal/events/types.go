// Package events defines MARSHAL's provider-neutral structured event contract.
// Persistence and delivery implementations must keep durable storage as the
// source of truth; this package intentionally contains only the contract.
package events

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

const maxDataBytes = 64 * 1024

// Sequence is the store-assigned, strictly increasing event position.
type Sequence uint64



// EventType is a closed, provider-neutral lifecycle vocabulary.
type EventType string

const (
	EventTypeAgentCreated             EventType = "agent.created"
	EventTypeAgentStarted             EventType = "agent.started"
	EventTypeAgentCompleted           EventType = "agent.completed"
	EventTypeAgentFailed              EventType = "agent.failed"
	EventTypeTaskCreated              EventType = "task.created"
	EventTypeTaskClaimed              EventType = "task.claimed"
	EventTypeTaskCompleted            EventType = "task.completed"
	EventTypeTaskFailed               EventType = "task.failed"
	EventTypeToolStarted              EventType = "tool.started"
	EventTypeToolCompleted            EventType = "tool.completed"
	EventTypeToolFailed               EventType = "tool.failed"
	EventTypePolicyAllowed            EventType = "policy.allowed"
	EventTypePolicyDenied             EventType = "policy.denied"
	EventTypeFileChanged              EventType = "file.changed"
	EventTypeTestStarted              EventType = "test.started"
	EventTypeTestPassed               EventType = "test.passed"
	EventTypeTestFailed               EventType = "test.failed"
	EventTypeVerificationPassed       EventType = "verification.passed"
	EventTypeVerificationFailed       EventType = "verification.failed"
	EventTypeApprovalRequested        EventType = "approval.requested"
	EventTypeApprovalGranted          EventType = "approval.granted"
	EventTypeApprovalDenied           EventType = "approval.denied"
	EventTypeCapabilityGrantRequested EventType = "capability.grant.requested"
	EventTypeCapabilityGrantIssued    EventType = "capability.grant.issued"
	EventTypeCapabilityGrantRevoked   EventType = "capability.grant.revoked"
	EventTypeCapabilityAllowed        EventType = "capability.authorize.allowed"
	EventTypeCapabilityDenied         EventType = "capability.authorize.denied"
	EventTypeDAGNodeAdded             EventType = "dag.node.added"
	EventTypeDAGEdgeAdded             EventType = "dag.edge.added"
	EventTypeDAGNodeReady             EventType = "dag.node.ready"
	EventTypeDAGNodeBlocked           EventType = "dag.node.blocked"
	EventTypeDAGCycleRejected         EventType = "dag.cycle.rejected"
	EventTypeCellPrepareStarted       EventType = "cell.prepare.started"
	EventTypeCellReady                EventType = "cell.ready"
	EventTypeCellExecStarted          EventType = "cell.exec.started"
	EventTypeCellExecFinished         EventType = "cell.exec.finished"
	EventTypeCellDestroyStarted       EventType = "cell.destroy.started"
	EventTypeCellDestroyed            EventType = "cell.destroyed"
	EventTypeCellFailed               EventType = "cell.failed"
	EventTypeAuthzAuthorityAllowed    EventType = "authz.authority.allowed"
	EventTypeAuthzAuthorityDenied     EventType = "authz.authority.denied"
	EventTypeGateAllowed              EventType = "gate.allowed"
	EventTypeGateDenied               EventType = "gate.denied"
	EventTypeGateBlocked              EventType = "gate.blocked"
	EventTypeGateDecisionInvalidated  EventType = "gate.decision.invalidated"
	EventTypeGateDecisionConsumed     EventType = "gate.decision.consumed"
	EventTypeSecretLeaseRequested     EventType = "secret.lease.requested"
	EventTypeSecretLeaseIssued        EventType = "secret.lease.issued"
	EventTypeSecretLeaseRevoked       EventType = "secret.lease.revoked"
	EventTypeSecretAccessUsed         EventType = "secret.access.used"
	EventTypeSecretAccessDenied       EventType = "secret.access.denied"
	EventTypeRiskAssessmentCreated    EventType = "risk.assessment.created"
	EventTypeRiskLevelHigh            EventType = "risk.level.high"
	EventTypeRiskLevelCritical        EventType = "risk.level.critical"
	EventTypeRiskOverrideDenied       EventType = "risk.override.denied"
	EventTypeNetworkEgressRequested   EventType = "network.egress.requested"
	EventTypeNetworkEgressAllowed     EventType = "network.egress.allowed"
	EventTypeNetworkEgressDenied      EventType = "network.egress.denied"
	EventTypeTrustContentRendered     EventType = "trustcontent.rendered"
	EventTypeTrustContentSegmentIngested EventType = "trustcontent.segment.ingested"
	EventTypeTrustContentZoneAssigned EventType = "trustcontent.zone.assigned"
	EventTypeTrustContentInjectionSuspected EventType = "trustcontent.injection.suspected"
	EventTypeHandoffCreated           EventType = "handoff.created"
	EventTypeHandoffAccepted          EventType = "handoff.accepted"
	EventTypeHandoffRejected          EventType = "handoff.rejected"
	EventTypeHandoffConsumed          EventType = "handoff.consumed"

	EventTypeAppended          EventType = "events.appended"
	EventTypeSubscriberDropped EventType = "events.subscriber.dropped"
	EventTypeSchemaRejected    EventType = "events.schema.rejected"
)

var eventTypes = map[EventType]struct{}{
	EventTypeAgentCreated: {}, EventTypeAgentStarted: {}, EventTypeAgentCompleted: {}, EventTypeAgentFailed: {},
	EventTypeTaskCreated: {}, EventTypeTaskClaimed: {}, EventTypeTaskCompleted: {}, EventTypeTaskFailed: {},
	EventTypeToolStarted: {}, EventTypeToolCompleted: {}, EventTypeToolFailed: {},
	EventTypePolicyAllowed: {}, EventTypePolicyDenied: {}, EventTypeFileChanged: {},
	EventTypeTestStarted: {}, EventTypeTestPassed: {}, EventTypeTestFailed: {},
	EventTypeVerificationPassed: {}, EventTypeVerificationFailed: {},
	EventTypeApprovalRequested: {}, EventTypeApprovalGranted: {}, EventTypeApprovalDenied: {},
	EventTypeCapabilityGrantRequested: {}, EventTypeCapabilityGrantIssued: {}, EventTypeCapabilityGrantRevoked: {},
	EventTypeCapabilityAllowed: {}, EventTypeCapabilityDenied: {},
	EventTypeDAGNodeAdded: {}, EventTypeDAGEdgeAdded: {}, EventTypeDAGNodeReady: {},
	EventTypeDAGNodeBlocked: {}, EventTypeDAGCycleRejected: {},
	EventTypeCellPrepareStarted: {}, EventTypeCellReady: {}, EventTypeCellExecStarted: {},
	EventTypeCellExecFinished: {}, EventTypeCellDestroyStarted: {}, EventTypeCellDestroyed: {},
	EventTypeCellFailed: {},
	EventTypeAuthzAuthorityAllowed: {}, EventTypeAuthzAuthorityDenied: {},
	EventTypeGateAllowed: {}, EventTypeGateDenied: {}, EventTypeGateBlocked: {},
	EventTypeGateDecisionInvalidated: {}, EventTypeGateDecisionConsumed: {},
	EventTypeSecretLeaseRequested: {}, EventTypeSecretLeaseIssued: {}, EventTypeSecretLeaseRevoked: {},
	EventTypeSecretAccessUsed: {}, EventTypeSecretAccessDenied: {},
	EventTypeRiskAssessmentCreated: {}, EventTypeRiskLevelHigh: {}, EventTypeRiskLevelCritical: {},
	EventTypeRiskOverrideDenied: {},
	EventTypeNetworkEgressRequested: {}, EventTypeNetworkEgressAllowed: {}, EventTypeNetworkEgressDenied: {},
	EventTypeTrustContentRendered: {}, EventTypeTrustContentSegmentIngested: {},
	EventTypeTrustContentZoneAssigned: {}, EventTypeTrustContentInjectionSuspected: {},
	EventTypeHandoffCreated: {}, EventTypeHandoffAccepted: {},
	EventTypeHandoffRejected: {}, EventTypeHandoffConsumed: {},
	EventTypeAppended: {}, EventTypeSubscriberDropped: {}, EventTypeSchemaRejected: {},
}

// Valid reports whether the type is in the canonical registry.
func (t EventType) Valid() bool { _, ok := eventTypes[t]; return ok }

// Event is the immutable semantic record exchanged by producers, stores and
// subscribers. Sequence is assigned by Store.Append; producers must not forge
// it. Data must contain bounded, non-sensitive references rather than payloads.
type Event struct {
	ID             string         `json:"event_id"`
	Sequence       Sequence       `json:"sequence"`
	Type           EventType      `json:"event_type"`
	Subject        string         `json:"subject,omitempty"`
	TaskID         string         `json:"task_id,omitempty"`
	RunID          string         `json:"run_id,omitempty"`
	ResourceID     string         `json:"resource_id,omitempty"`
	EvidenceID     string         `json:"evidence_id,omitempty"`
	At             time.Time      `json:"at"`
	Data           map[string]any `json:"data,omitempty"`
	IdempotencyKey string         `json:"idempotency_key,omitempty"`
}



// Validate checks the contract boundary before a store or bus can observe an
// event. Secret-bearing field names are rejected rather than silently stored.
func (e Event) Validate() error {
	if !e.Type.Valid() {
		return ErrInvalidType
	}
	for key := range e.Data {
		if forbiddenField(key) {
			return ErrSecretField
		}
	}
	if encoded, err := json.Marshal(e.Data); err != nil || len(encoded) > maxDataBytes {
		return ErrInvalidEvent
	}
	return nil
}

func forbiddenField(key string) bool {
	key = strings.ToLower(strings.ReplaceAll(key, "-", "_"))
	for _, marker := range []string{"secret", "password", "passwd", "token", "credential", "api_key", "private_key"} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	return false
}

// Store is the durable event history boundary. Since returns events strictly
// after the supplied sequence so reconnecting consumers can resume safely.
type Store interface {
	Append(context.Context, Event) (Event, error)
	Since(context.Context, Sequence) ([]Event, error)
}

// Subscription is a read-only live stream. Implementations must not expose a
// send-capable channel to consumers and must provide deterministic cleanup.
type Subscription struct {
	Events <-chan Event
	Close  func()
}

// Bus publishes only after Store.Append has durably committed the event.
type Bus interface {
	Publish(context.Context, Event) error
	Subscribe(context.Context, Sequence) (Subscription, error)
}

