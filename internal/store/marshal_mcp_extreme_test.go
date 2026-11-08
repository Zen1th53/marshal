//go:build !governed

package store

import (
	"context"
	"strings"
	"testing"
)

func TestMCPProposalDurableAtomicAuditAndIdempotency(t *testing.T) {
	s := projectStore(t)
	ctx := context.Background()
	if err := s.InitMCPProposals(ctx); err != nil {
		t.Fatal(err)
	}
	p := MCPProposal{ID: "mcp:test", Launch: "launch", Owner: "owner", Provider: "codex", Submission: "s1", Payload: `{"action": "intake", "language":"uz"}`}
	admitted, fresh, err := s.AdmitMCPProposal(ctx, "PROJECT-local", p)
	if err != nil || !fresh {
		t.Fatalf("admission: %+v %v", admitted, err)
	}
	duplicate, fresh, err := s.AdmitMCPProposal(ctx, "PROJECT-local", p)
	if err != nil || fresh || duplicate.ID != p.ID {
		t.Fatalf("duplicate: %+v %v", duplicate, err)
	}
	p.Payload = strings.Replace(p.Payload, "uz", "en", 1)
	if _, _, err = s.AdmitMCPProposal(ctx, "PROJECT-local", p); err == nil {
		t.Fatal("conflicting replay accepted")
	}
	if err = s.SetMCPProposalState(ctx, "PROJECT-local", p.ID, "answer_accepted", "answer journaled"); err != nil {
		t.Fatal(err)
	}
	if err = s.SetMCPProposalState(ctx, "PROJECT-local", p.ID, "applying", "effect admitted"); err != nil {
		t.Fatal(err)
	}
	if err = s.ExpireMCPLaunch(ctx, "PROJECT-local", "launch"); err != nil {
		t.Fatal(err)
	}
	after, err := s.MCPProposal(ctx, "PROJECT-local", p.ID)
	if err != nil || after.State != "uncertain" {
		t.Fatalf("crash/revoke result: %+v %v", after, err)
	}
	var count int
	if err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM extreme_mcp_proposal_audit WHERE project_id=? AND occurrence_id=?`, "PROJECT-local", p.ID).Scan(&count); err != nil || count != 4 {
		t.Fatalf("audit count=%d err=%v", count, err)
	}
	if _, fresh, err = s.AdmitMCPProposal(ctx, "PROJECT-local", admitted); err != nil || fresh {
		t.Fatalf("terminal replay: %v %v", fresh, err)
	}
}
func TestMCPProposalAdmissionFailureHasNoPartialTrust(t *testing.T) {
	s := projectStore(t)
	ctx := context.Background()
	if err := s.InitMCPProposals(ctx); err != nil {
		t.Fatal(err)
	}
	// Force the last atomic write to fail after source/legacy insertion.
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER reject_mcp_audit BEFORE INSERT ON extreme_mcp_proposal_audit BEGIN SELECT RAISE(ABORT,'injected audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	p := MCPProposal{ID: "mcp:failure", Launch: "launch", Owner: "owner", Provider: "codex", Submission: "s1", Payload: `{"action":"intake","language":"uz"}`}
	if _, _, err := s.AdmitMCPProposal(ctx, "PROJECT-local", p); err == nil {
		t.Fatal("admission claimed success")
	}
	for _, table := range []string{"marshal_proposals", "extreme_mcp_proposals", "extreme_mcp_proposal_audit"} {
		var n int
		if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("partial trust in %s: %d %v", table, n, err)
		}
	}
}
