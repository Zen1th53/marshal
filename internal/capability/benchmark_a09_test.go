package capability

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func BenchmarkAuthorizeLocalState(b *testing.B) {
	for _, size := range []int{1, 100, 1000} {
		b.Run(fmt.Sprintf("cases-%d", size), func(b *testing.B) {
			now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
			repo := &memoryGrantRepository{grants: make(map[GrantID]Grant, size)}
			for i := 0; i < size; i++ {
				grant := Grant{
					ID: GrantID(fmt.Sprintf("grant-%04d", i)), Subject: "agent-1", TaskID: "task-1",
					Kind: KindFilesystemRead, Scope: Scope{Resource: fmt.Sprintf("/workspace/file-%04d", i), Actions: []string{"read"}},
					IssuedAt: now, ExpiresAt: now.Add(time.Hour), Issuer: "broker",
				}
				repo.grants[grant.ID] = grant
			}
			engine := NewAuthorizedEngine(repo, func() time.Time { return now }, testAuthority{})
			query := Query{Subject: "agent-1", TaskID: "task-1", Kind: KindFilesystemRead, Resource: fmt.Sprintf("/workspace/file-%04d", size-1), Action: "read", At: now}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := engine.Authorize(context.Background(), query); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
