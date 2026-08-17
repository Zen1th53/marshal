package capability

import (
	"context"
	"errors"
	"testing"
	"time"
)

type cancellingAuthority struct{}

func (cancellingAuthority) AuthorizeGrant(ctx context.Context, _ GrantRequest) error {
	<-ctx.Done()
	return ctx.Err()
}
func (cancellingAuthority) AuthorizeRevoke(ctx context.Context, _ RevokeRequest, _ Grant) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestEngineCancellationPropagatesBeforeDurableMutation(t *testing.T) {
	now := time.Date(2026, 8, 16, 18, 0, 0, 0, time.UTC)
	repo := &memoryGrantRepository{grants: map[GrantID]Grant{}}
	engine := NewAuthorizedEngine(repo, func() time.Time { return now }, cancellingAuthority{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := engine.Grant(ctx, GrantRequest{
		Subject: "agent-a08", TaskID: "task-a08", Kind: KindGitCommit,
		Scope:     Scope{Resource: "repo-a08", Actions: []string{"commit"}},
		ExpiresAt: now.Add(time.Hour), Issuer: "broker", IdempotencyKey: "a08-cancel",
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error=%v, want context.Canceled", err)
	}
	if len(repo.grants) != 0 {
		t.Fatalf("cancellation mutated repository: %d rows", len(repo.grants))
	}
}
