package tui

import (
	"context"
	"github.com/Zen1th53/marshal/internal/cloud"
)

// PersistedEntitlementRequestReader is optional and observation-only. A missing reader,
// missing server ID, or an in-flight attempt must be rendered with a reasoned
// UNKNOWN. No request state, including "active", proves a verified lease.
type PersistedEntitlementRequestReader interface {
	PersistedEntitlementRequest(context.Context) (cloud.RequestObservation, bool, error)
}

func requestPresentation(o cloud.RequestObservation, found bool) EntitlementRequestObservation {
	if !found {
		return EntitlementRequestObservation{State: "UNKNOWN", Error: "no locally recorded entitlement request"}
	}
	result := EntitlementRequestObservation{LocalAttemptID: o.LocalAttemptID, ServerRequestID: o.ServerRequestID, HasServerID: o.ServerRequestID != "", State: o.State, RequestedAt: o.AttemptedAt, Error: o.ErrorCode, Endpoint: o.Key.Endpoint, InstallationID: o.Key.InstallationID, SessionID: o.Key.SessionID, Recorded: true}
	if o.ObservedAt != nil {
		result.UpdatedAt = *o.ObservedAt
	}
	if o.State == "sending" {
		result.State = "UNKNOWN"
		result.Error = "local attempt persisted; server outcome is unknown (interrupted or in flight)"
	}
	return result
}
