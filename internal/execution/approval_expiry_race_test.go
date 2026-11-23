package execution

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestApprovalExpiryRacesDecisionOnce(t *testing.T) {
	dir := t.TempDir()
	mgr := NewApprovalManager(dir)

	base := time.Now().UTC().Truncate(time.Second)
	for round := 0; round < 32; round++ {
		req, err := mgr.RequestApproval(ApprovalRequest{
			RequestedBy:    "worker-race",
			RunID:          "run-race",
			TaskID:         "task-race",
			PlanID:         "plan-race",
			PlanVersion:    1,
			Attempt:        round + 1,
			OperationType:  "TASK_EXECUTE",
			TargetResource: "README.md",
			RiskLevel:      model.R2,
			Scope:          "task_mutation",
			Parameters:     "race test",
			TTL:            10 * time.Minute,
			Now:            base,
		})
		if err != nil {
			t.Fatalf("round %d request: %v", round, err)
		}
		if req.Revision != 1 || req.ScopeDigest == "" || req.Attempt != round+1 {
			t.Fatalf("round %d identity missing fields: %+v", round, req)
		}

		// Race concurrent approve (just before deadline) vs expire (at deadline).
		approveAt := req.ExpiresAt.Add(-time.Second)
		expireAt := *req.ExpiresAt

		start := make(chan struct{})
		var wg sync.WaitGroup
		var decideErr error
		var decideRes *RuntimeApproval
		var expireRes *RuntimeApproval
		var expiredWon bool
		var expireErr error

		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			decideRes, decideErr = mgr.Decide(req.ApprovalID, true, "operator-race", "approve race", approveAt)
		}()
		go func() {
			defer wg.Done()
			<-start
			expireRes, expiredWon, expireErr = mgr.Expire(req.ApprovalID, expireAt)
		}()
		close(start)
		wg.Wait()

		if expireErr != nil {
			t.Fatalf("round %d expire error: %v", round, expireErr)
		}

		approveWon := decideErr == nil
		if approveWon == expiredWon {
			t.Fatalf("round %d expected strictly one winner: approveWon=%v (err=%v) expiredWon=%v", round, approveWon, decideErr, expiredWon)
		}

		final, err := mgr.GetApproval(req.ApprovalID)
		if err != nil {
			t.Fatalf("round %d get final: %v", round, err)
		}
		if final.Revision != 2 {
			t.Fatalf("round %d expected single terminal transition (rev 2), got rev %d status %s", round, final.Revision, final.Status)
		}
		if approveWon {
			if final.Status != ApprovalApproved || final.ApprovedBy != "operator-race" {
				t.Fatalf("round %d approve won but final=%+v", round, final)
			}
			if expireRes == nil || expireRes.Status != ApprovalApproved {
				t.Fatalf("round %d expire loser did not observe APPROVED: %+v", round, expireRes)
			}
		} else {
			if final.Status != ApprovalExpired || final.ApprovedBy != "" {
				t.Fatalf("round %d expire won but final=%+v", round, final)
			}
			if decideRes == nil || decideRes.Status != ApprovalExpired {
				t.Fatalf("round %d decide loser did not observe EXPIRED: %+v", round, decideRes)
			}
		}

		// A late decision after expiry or approval must never overwrite the terminal receipt.
		receiptPath := filepath.Join(dir, req.ApprovalID+".json")
		beforeBytes, err := os.ReadFile(receiptPath)
		if err != nil {
			t.Fatalf("round %d read receipt: %v", round, err)
		}
		late, lateErr := mgr.Decide(req.ApprovalID, false, "operator-late", "late deny", req.ExpiresAt.Add(time.Minute))
		if lateErr == nil {
			t.Fatalf("round %d late decision unexpectedly succeeded", round)
		}
		if late == nil || late.Status != final.Status || late.Revision != final.Revision {
			t.Fatalf("round %d late decision altered state: got=%+v want=%+v", round, late, final)
		}
		afterBytes, err := os.ReadFile(receiptPath)
		if err != nil || !bytes.Equal(beforeBytes, afterBytes) {
			t.Fatalf("round %d late decision rewrote receipt bytes", round)
		}
	}
}
