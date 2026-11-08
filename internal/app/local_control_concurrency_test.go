package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/capability"
)

func TestOpenLocalControlConcurrentProvisionKeepsImmutableGrants(t *testing.T) {
	r := runtimeForPlan(t)
	const callers = 12
	start := make(chan struct{})
	errs := make([]error, callers)
	var workers sync.WaitGroup
	workers.Add(callers)
	for i := range errs {
		go func() {
			defer workers.Done()
			<-start
			_, errs[i] = r.OpenLocalControl(context.Background())
		}()
	}
	close(start)
	workers.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent owner %d: %v", i, err)
		}
	}
	grants, err := r.Store().ListCapabilityGrants(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var goalCreate capability.Grant
	for _, grant := range grants {
		if len(grant.Scope.Actions) == 1 && grant.Scope.Actions[0] == "goal.create" {
			if goalCreate.ID != "" {
				t.Fatal("concurrent provisioning duplicated the goal.create grant")
			}
			goalCreate = grant
		}
	}
	if goalCreate.ID == "" {
		t.Fatal("concurrent provisioning omitted the goal.create grant")
	}
	if err := r.Store().RevokeCapabilityGrant(t.Context(), goalCreate.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.OpenLocalControl(t.Context()); err != nil {
		t.Fatal(err)
	}
	readback, err := r.Store().GetCapabilityGrant(t.Context(), goalCreate.ID)
	if err != nil || readback.RevokedAt == nil {
		t.Fatalf("reopening resurrected a revoked local grant: %+v, %v", readback, err)
	}
}
