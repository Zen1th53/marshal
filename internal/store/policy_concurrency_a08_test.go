package store

import (
	"context"
	"sync"
	"testing"

	"github.com/Zen1th53/marshal/internal/policy"
)

func openStoreAt(t *testing.T, path string) *Store {
	t.Helper()
	st, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestA08ConcurrentActivationCannotCreateAmbiguousActiveSet(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/policy.db"
	first := openStoreAt(t, path)
	second := openStoreAt(t, path)
	if err := first.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	records := []PolicyRecord{testPolicyRecord(t, "active-a"), testPolicyRecord(t, "active-b")}
	for i := range records {
		records[i].Policy.Version = 1
		if err := first.PutPolicy(ctx, records[i]); err != nil {
			t.Fatal(err)
		}
		loaded := authorizedRequest(records[i])
		if _, err := first.TransitionPolicyStateAuthorized(ctx, loaded, policyMutationAuthorizer(func(context.Context, policy.PolicyMutationRequest) (policy.PolicyMutationDecision, error) {
			return allowedDecision(loaded), nil
		})); err != nil {
			t.Fatal(err)
		}
		validated := records[i]
		validated.State = policy.StateValidated
		records[i] = validated
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i, st := range []*Store{first, second} {
		wg.Add(1)
		go func(i int, st *Store) {
			defer wg.Done()
			<-start
			req := policy.PolicyMutationRequest{SubjectID: "subject", SessionID: "session", TaskID: "task", ChangeID: "change", PolicyID: records[i].Policy.ID, PolicyVersion: 1, ExpectedState: policy.StateValidated, TargetState: policy.StateActive, Binding: records[i].Binding, Action: policy.ActionPolicyActivate}
			_, err := st.TransitionPolicyStateAuthorized(ctx, req, policyMutationAuthorizer(func(context.Context, policy.PolicyMutationRequest) (policy.PolicyMutationDecision, error) {
				return allowedDecision(req), nil
			}))
			results <- err
		}(i, st)
	}
	close(start)
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("activation winners = %d, want 1", winners)
	}
	var active int
	if err := first.db.QueryRowContext(ctx, "SELECT count(*) FROM policy_versions WHERE state = ?", string(policy.StateActive)).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 1 {
		t.Fatalf("active policies = %d, want 1", active)
	}
}

func TestA08PolicyStoreRejectsSecondActivePolicy(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	first := testPolicyRecord(t, "unique-active-a")
	first.State = policy.StateActive
	if err := st.PutPolicy(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := testPolicyRecord(t, "unique-active-b")
	second.State = policy.StateActive
	if err := st.PutPolicy(ctx, second); err == nil {
		t.Fatal("second active policy accepted")
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM policy_versions WHERE state = ?", string(policy.StateActive)); got != 1 {
		t.Fatalf("active policies = %d, want 1", got)
	}
}
