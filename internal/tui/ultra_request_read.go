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

// StoredEntitlementRequestReader is composed with the exact identity of the
// workspace; identities are never inferred from the latest file on disk.
type StoredEntitlementRequestReader struct {
	Store *cloud.RequestStore
	Key   cloud.RequestKey
}

func (r StoredEntitlementRequestReader) PersistedEntitlementRequest(ctx context.Context) (cloud.RequestObservation, bool, error) {
	if r.Store == nil {
		return cloud.RequestObservation{}, false, cloud.ErrState
	}
	return r.Store.Read(ctx, r.Key)
}

// EntitlementRequest adapts E's persistence DTO to F's presentation DTO. A
// zero server timestamp remains zero; it is never replaced with local time.
func (r StoredEntitlementRequestReader) EntitlementRequest(ctx context.Context) (EntitlementRequestObservation, error) {
	o, found, err := r.PersistedEntitlementRequest(ctx)
	return requestPresentation(o, found), err
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
