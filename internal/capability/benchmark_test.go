package capability

import (
	"context"
	"testing"
	"time"
)

func BenchmarkAuthorizeInMemoryGrant(b *testing.B) {
	repo := newMemoryRepository()
	engine := NewEngine(repo, func() time.Time { return time.Unix(100, 0).UTC() }, allowAuthority{})
	grant, err := engine.Grant(context.Background(), GrantRequest{Subject: "agent-1", TaskID: "task-1", Kind: KindFilesystemRead, Scope: Scope{Resource: "/workspace", Actions: []string{"read"}}, TTL: time.Hour, Issuer: "admin"})
	if err != nil {
		b.Fatal(err)
	}
	_ = grant
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		decision, err := engine.Authorize(context.Background(), Query{Subject: "agent-1", TaskID: "task-1", Kind: KindFilesystemRead, Resource: "/workspace", Action: "read"})
		if err != nil || !decision.Allowed {
			b.Fatalf("decision=%#v err=%v", decision, err)
		}
	}
}
