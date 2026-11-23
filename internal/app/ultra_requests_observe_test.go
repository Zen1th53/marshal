package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Zen1th53/marshal/internal/cloud"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestObserveEntitlementCommandAcrossWorkspaceSessions(t *testing.T) {
	r := runtimeForPlan(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		if q.URL.Path == "/v1/entitlements/request" {
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "pending"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"nonce": "test-nonce"})
	}))
	defer server.Close()
	client, err := cloud.NewClient(server.URL, "test")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := cloud.NewInstallation()
	if err != nil {
		t.Fatal(err)
	}
	requests := cloud.NewRequestStore(t.TempDir())
	old, err := NewULTRARequestService(r, client, identity, "old-workspace", "old-cloud-session", requests)
	if err != nil {
		t.Fatal(err)
	}
	target, err := old.Target(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	e := CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: target.WorkspaceSessionID, TargetID: model.SessionRecoveryDigest(target.Key), ExpectedVersion: target.ExpectedGeneration, IdempotencyKey: "exact-request"}
	raw, _ := json.Marshal(struct {
		Envelope CommandEnvelope
		Target   EntitlementRequestTarget
	}{e, target})
	sum := sha256.Sum256(raw)
	// Simulate an interrupted command journal without requiring a live lease.
	_, _ = client.RequestEntitlementTrackedCommand(context.Background(), identity, target.Key.SessionID, requests, 0, hex.EncodeToString(sum[:]))
	current, err := NewULTRARequestService(r, client, identity, "new-workspace", "new-cloud-session", requests)
	if err != nil {
		t.Fatal(err)
	}
	o, found, err := current.ObserveCommand(t.Context(), e, target)
	if err != nil || !found || o.LocalAttemptID == "" {
		t.Fatal(o, found, err)
	}
	e.IdempotencyKey = "different-request"
	if _, found, err = current.ObserveCommand(t.Context(), e, target); err == nil || found {
		t.Fatal("foreign command matched the journal")
	}
}
