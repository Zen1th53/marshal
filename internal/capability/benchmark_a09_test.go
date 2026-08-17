package capability

import (
	"context"
	"testing"
	"time"
)

func BenchmarkAuthorizeCapability(b *testing.B) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	repo := &memoryGrantRepository{grants: map[GrantID]Grant{}}
	engine := NewAuthorizedEngine(repo, func() time.Time { return now }, testAuthority{})
	_, err := engine.Grant(context.Background(), GrantRequest{Subject: "agent", TaskID: "task", Kind: KindShellExec, Scope: Scope{Resource: "/bin/sh", Actions: []string{"exec"}}, ExpiresAt: now.Add(time.Hour), Issuer: "admin", IdempotencyKey: "bench"})
	if err != nil {
		b.Fatal(err)
	}
	query := Query{Subject: "agent", TaskID: "task", Kind: KindShellExec, Resource: "/bin/sh", Action: "exec", At: now}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		decision, err := engine.Authorize(context.Background(), query)
		if err != nil || decision.Outcome != OutcomeAllow {
			b.Fatalf("decision=%#v err=%v", decision, err)
		}
	}
}
