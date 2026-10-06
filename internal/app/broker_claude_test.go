package app

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/netpolicy"
)

func TestBrokerClaudeExpiryOperatorNotification(t *testing.T) {
	r := openNetpolRuntime(t)
	ctx := context.Background()
	scope := &runEgress{id: "claude-test", provider: "claude", worker: "claude-worker", pending: map[string]bool{}}
	var delivered []EgressAlert
	r.SetEgressAlertSink(func(a EgressAlert) error { delivered = append(delivered, a); return nil })
	if err := r.recordEgressAttempt(ctx, scope, "api.anthropic.com", 443, netpolicy.Decision{Reason: netpolicy.ReasonClaudeSignInExpired}); err != nil {
		t.Fatal(err)
	}
	if len(delivered) != 1 || delivered[0].Message != netpolicy.ClaudeSignInExpiredMessage {
		t.Fatal("missing fixed operator alert")
	}
	queued, err := r.EgressNotifications(ctx)
	if err != nil || len(queued) != 1 || queued[0].Message != netpolicy.ClaudeSignInExpiredMessage {
		t.Fatal("missing durable operator alert")
	}
	if r.HasCredentialGrant(ctx, "claude") {
		t.Fatal("expiry notification granted credentials")
	}
}
