package capability

import (
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/evidence"
)

func TestObservedEngineRecordsBoundedAuthorizationMetrics(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	repo := &memoryGrantRepository{grants: map[GrantID]Grant{}}
	metrics := evidence.NewMetricsRecorder()
	engine := NewObservedEngine(repo, func() time.Time { return now }, testAuthority{}, metrics)
	grant, err := engine.Grant(nil, GrantRequest{
		Subject: "agent-1", TaskID: "task-1", Kind: KindShellExec,
		Scope:     Scope{Resource: "/tmp/job", Actions: []string{"execute"}},
		ExpiresAt: now.Add(time.Hour), Issuer: "broker", IdempotencyKey: "metrics-a09",
	})
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	if _, err := engine.Authorize(nil, Query{Subject: grant.Subject, TaskID: grant.TaskID, Kind: grant.Kind, Resource: grant.Scope.Resource, Action: "execute", At: now}); err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if _, err := engine.Authorize(nil, Query{Subject: "agent-1", TaskID: "task-1", Kind: KindShellExec, Resource: "/tmp/other", Action: "execute", At: now}); err != nil {
		t.Fatalf("deny should be a decision, not a backend error: %v", err)
	}

	snapshot := metrics.Snapshot()
	if snapshot.Success[evidence.MetricOperationCapability] != 1 {
		t.Fatalf("allow count = %d", snapshot.Success[evidence.MetricOperationCapability])
	}
	if snapshot.Denied["CAP_DENIED"] != 1 {
		t.Fatalf("deny count = %d", snapshot.Denied["CAP_DENIED"])
	}
	if snapshot.DurationNanoseconds[evidence.MetricOperationCapability] == 0 {
		t.Fatal("authorization duration was not recorded")
	}
}

func TestRevokedGrantRetryStillRequiresTheOriginalActor(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	repo := &memoryGrantRepository{grants: map[GrantID]Grant{}}
	engine := NewAuthorizedEngine(repo, func() time.Time { return now }, testAuthority{})
	grant, err := engine.Grant(nil, GrantRequest{
		Subject: "agent-1", TaskID: "task-1", Kind: KindShellExec,
		Scope:     Scope{Resource: "/tmp/job", Actions: []string{"execute"}},
		ExpiresAt: now.Add(time.Hour), Issuer: "broker", IdempotencyKey: "revoke-a09",
	})
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	if err := engine.Revoke(nil, RevokeRequest{GrantID: grant.ID, Actor: "broker"}); err != nil {
		t.Fatalf("initial revoke: %v", err)
	}
	if err := engine.Revoke(nil, RevokeRequest{GrantID: grant.ID, Actor: "foreign-actor"}); err == nil {
		t.Fatal("foreign actor retried revoked grant without authorization")
	}
}
