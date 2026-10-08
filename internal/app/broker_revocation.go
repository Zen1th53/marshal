package app

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// A closure is durable owner evidence, bound to the scope incarnation and (for
// revocation) the exact decision. Ending a scope also acknowledges its exchanges.
func (r *Runtime) recordBrokerClosed(ctx context.Context, scope *runEgress, decision string) error {
	scope.mu.Lock()
	defer scope.mu.Unlock()
	id := fmt.Sprintf("EVENT-BROKER-CLOSED-%x", sha256.Sum256([]byte(scope.socket+"\x00"+decision)))
	// Repeated Close calls must not rewrite an acknowledgement's timestamp.
	records, err := r.store.ListEvents(ctx)
	if err != nil {
		return err
	}
	for _, event := range records {
		if event.ID == id {
			return nil
		}
	}
	return r.store.AppendEvent(ctx, nil, model.Event{ID: id, Type: "BROKER_SCOPE_CLOSED", ProjectID: r.ProjectID(), Timestamp: time.Now().UTC(), Data: map[string]any{"scope_socket": scope.socket, "provider": scope.provider, "decision_id": decision}})
}

// CredentialRevocationStatus never infers closure from a dead socket. An owner
// that has disappeared without acknowledging remains pending, including after
// reopening the operator UI. A later grant cannot hide an unacknowledged revoke.
func (r *Runtime) CredentialRevocationStatus(ctx context.Context, provider string) (string, error) {
	if r == nil || r.store == nil {
		return "", model.ErrUnavailable
	}
	if !credentialProvider(provider) {
		return "", model.ErrInvalid
	}
	records, err := r.store.ListEvents(ctx)
	if err != nil {
		return "", err
	}
	active, pending := map[string]bool{}, map[string]bool{}
	revoked := false
	for _, event := range records {
		socket, _ := event.Data["scope_socket"].(string)
		if event.Type == "network.egress.requested" && event.Data["source"] == "run scope" && event.Data["provider"] == provider && event.Data["brokered"] == true {
			active[socket] = true
		}
		if event.Type == "PERMISSION_DECIDED" && event.ProjectID == r.ProjectID() && event.Data["kind"] == "credential" && event.Data["object"] == provider && event.Data["allow"] == false {
			revoked = true
			for scope := range active {
				pending[scope] = true
			}
		}
		if event.Type == "BROKER_SCOPE_CLOSED" && event.ProjectID == r.ProjectID() && event.Data["provider"] == provider {
			delete(active, socket)
			delete(pending, socket)
		}
	}
	if len(pending) > 0 {
		return "pending", nil
	}
	if revoked {
		return "closed", nil
	}
	return "none", nil
}
