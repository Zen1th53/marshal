//go:build !governed

package app

import (
	"encoding/json"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/statepath"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIndependentProviderTransportExpiredAndDeniedRefuseFreshConsume(t *testing.T) {
	for _, state := range []string{"expired", "denied"} {
		t.Run(state, func(t *testing.T) {
			r, task, pkg, canonical, agent := transportFixture(t)
			_, ctx, id := fixtureTransportApproval(t, r, task, pkg, canonical, agent, true)
			approval, err := r.Execution().Engine().ApprovalManager().GetApproval(id)
			if err != nil {
				t.Fatal(err)
			}
			if state == "expired" {
				past := time.Now().Add(-time.Minute)
				approval.ExpiresAt = &past
			} else {
				approval.Status = execution.ApprovalDenied
			}
			raw, err := json.Marshal(approval)
			if err != nil {
				t.Fatal(err)
			}
			// Fresh disposable test fixture only; force persisted lifecycle change then fresh refresh.
			path := filepath.Join(r.ProjectRoot(), statepath.DirName, "approvals", id+".json")
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			if err := r.consumeReviewedProviderTransport(ctx, "127.0.0.1:11434", "refused-lifecycle"); err == nil {
				t.Fatal("expired/denied approval consumed")
			}
			actual, err := r.Execution().Engine().ApprovalManager().GetApproval(id)
			if err != nil {
				t.Fatal(err)
			}
			if actual.Status == execution.ApprovalConsumed {
				t.Fatal("refused lifecycle marked consumed")
			}
		})
	}
}
