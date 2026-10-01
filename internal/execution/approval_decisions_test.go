package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"
)

func TestDurableApprovalBindingAndOneTimeDecision(t *testing.T) {
	dir := t.TempDir()
	first := NewApprovalManager(dir)
	second := NewApprovalManager(dir)
	a, err := first.RequestApproval(ApprovalRequest{RunID: "run", TaskID: "task", RequestedBy: "worker", OperationType: "write", TargetResource: "file", Parameters: "exact parameters", CurrentState: "exact state"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := second.GetApproval(a.ApprovalID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(snapshot)
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	bad := WithApprovalDecision(context.Background(), "stale", "wrong-command")
	if _, err := first.DecideContext(bad, a.ApprovalID, true, "owner", "reviewed", time.Now()); err == nil {
		t.Fatal("stale digest accepted")
	}
	ctx := WithApprovalDecision(context.Background(), digest, "exact-command")
	if _, err := first.DecideContext(ctx, a.ApprovalID, true, "worker", "self", time.Now()); err == nil {
		t.Fatal("requester approved its own action")
	}
	if _, err := first.DecideContext(ctx, a.ApprovalID, true, "owner", "reviewed", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := second.DecideContext(ctx, a.ApprovalID, false, "other-owner", "stale cache", time.Now()); err == nil {
		t.Fatal("second manager re-decided cached pending record")
	}
	fresh, err := second.GetApproval(a.ApprovalID)
	if err != nil || fresh.Status != ApprovalApproved || fresh.DecisionCommandKey != "exact-command" {
		t.Fatalf("durable readback: %+v %v", fresh, err)
	}
	if err := second.ValidateAndConsume(a.ApprovalID, a.ActionDigest, a.StateDigest, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := first.ValidateAndConsume(a.ApprovalID, a.ActionDigest, a.StateDigest, time.Now()); err == nil {
		t.Fatal("second consumption accepted")
	}
	third := NewApprovalManager(dir)
	read, err := third.GetApproval(a.ApprovalID)
	if err != nil || read.Status != ApprovalConsumed {
		t.Fatalf("durable consumption: %+v %v", read, err)
	}
}

func TestApprovalRequesterUsesPrincipalIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, requester, run, task string
		denied                     bool
	}{
		{"different requester with matching resource ids", "worker", "owner", "owner", false},
		{"owner requester", "owner", "run", "task", true},
		{"run requester fallback", "", "owner", "task", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := NewApprovalManager()
			a, err := manager.RequestApproval(ApprovalRequest{RunID: tc.run, TaskID: tc.task, RequestedBy: tc.requester, OperationType: "write"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = manager.Decide(a.ApprovalID, true, "owner", "reviewed", time.Now())
			if tc.denied && err == nil {
				t.Fatal("self approval accepted")
			}
			if !tc.denied && err != nil {
				t.Fatalf("resource id mistaken for requester: %v", err)
			}
		})
	}
}
