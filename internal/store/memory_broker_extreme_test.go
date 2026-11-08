//go:build !governed

package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/Zen1th53/marshal/internal/memory/security"
	"github.com/Zen1th53/marshal/internal/model"
	"testing"
	"time"
)

func TestMCPMemoryManagedLookupIsCanonicalProjectScopedAndSurvivesExpiry(t *testing.T) {
	ordinary := projectStore(t)
	if _, err := ordinary.MCPMemoryByRecordID(t.Context(), "PROJECT-local", "MEM-caller-live-mcp"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("uninitialized ordinary runtime changed", err)
	}
	s, c := memoryBrokerFixture(t)
	for _, project := range []string{"PROJECT-local", "PROJECT-other"} {
		got, err := s.MCPMemoryByRecordID(t.Context(), project, c.Record.ID)
		if project != "PROJECT-local" {
			if !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("lookup crossed project scope", got, err)
			}
		} else if err != nil || got.Occurrence != c.Occurrence {
			t.Fatal(got, err)
		}
	}
	if err := s.ExpireMCPLaunch(t.Context(), "PROJECT-local", "launch"); err != nil {
		t.Fatal(err)
	}
	got, err := s.MCPMemoryByRecordID(t.Context(), "PROJECT-local", c.Record.ID)
	if err != nil || got.State != "expired" || got.Record.ID != c.Record.ID {
		t.Fatal("expiry erased broker origin", got, err)
	}
	if _, err := s.MCPMemoryByRecordID(t.Context(), "PROJECT-local", "MEM-forged-prefix"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("prefix became source proof", err)
	}
}

func memoryBrokerFixture(t *testing.T) (*Store, MCPMemoryCandidate) {
	t.Helper()
	s := projectStore(t)
	if err := s.InitMCPProposals(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := s.InitMCPMemory(t.Context()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	rec := model.MemoryRecordV2{ID: "MEM-broker-test", ProjectID: "PROJECT-local", Kind: model.MemoryKindSemantic, Lifecycle: model.MemoryCandidate, Confidence: model.ConfidenceInferred, Authority: model.AuthorityAgent, Title: "source memory", Body: "quota reserve observation", Scope: "project", ScopeID: "PROJECT-local", ObservedAt: now, IngestedAt: now, ValidFrom: now, CreatedAt: now, UpdatedAt: now, Source: model.MemorySource{Kind: "live_mcp", Reference: "runtime-owner"}, ExtMeta: map[string]any{"provider": "codex", "launch_id": "launch", "source_claims": []string{"historical Claude note"}}}
	raw, _ := json.Marshal(map[string]string{"action": "memory", "id": rec.ID})
	c, _, err := s.AdmitMCPMemory(t.Context(), "PROJECT-local", MCPProposal{ID: "mcp:memory-test", Launch: "launch", Owner: "owner", Provider: "codex", Submission: "topic", Payload: string(raw)}, MCPMemoryCandidate{ImportKey: "topic", PayloadDigest: "payload-digest", Record: rec})
	if err != nil {
		t.Fatal(err)
	}
	return s, c
}
func memoryBrokerAudit() model.Event {
	return model.Event{ID: "EVENT-memory-test", ProjectID: "PROJECT-local", Type: "PERMISSION_DECIDED", Timestamp: time.Now().UTC(), Data: map[string]any{"allow": true, "source": "marshal"}}
}
func TestMCPMemoryTransactionFaultsRollbackAndSafeRetry(t *testing.T) {
	for _, trigger := range []string{
		`CREATE TRIGGER fail_memory BEFORE INSERT ON memory_records_v2 BEGIN SELECT RAISE(ABORT,'canonical failure'); END`,
		`CREATE TRIGGER fail_memory BEFORE INSERT ON memory_outbox BEGIN SELECT RAISE(ABORT,'outbox failure'); END`,
		`CREATE TRIGGER fail_memory BEFORE INSERT ON audit_events WHEN NEW.event_type='PERMISSION_DECIDED' BEGIN SELECT RAISE(ABORT,'decision audit failure'); END`,
		`CREATE TRIGGER fail_memory BEFORE UPDATE ON extreme_mcp_proposals WHEN NEW.state='committed' BEGIN SELECT RAISE(ABORT,'receipt failure'); END`,
	} {
		t.Run(trigger[40:55], func(t *testing.T) {
			s, c := memoryBrokerFixture(t)
			var sync int
			if err := s.db.QueryRowContext(t.Context(), "PRAGMA synchronous").Scan(&sync); err != nil || sync != 2 {
				t.Fatalf("durability=%d %v", sync, err)
			}
			if _, err := s.db.ExecContext(t.Context(), trigger); err != nil {
				t.Fatal(err)
			}
			if err := s.CommitMCPMemoryDecision(t.Context(), "PROJECT-local", c.Occurrence, true, c.Record, memoryBrokerAudit()); err == nil {
				t.Fatal("fault reported success")
			}
			for _, table := range []string{"memory_records_v2", "memory_outbox"} {
				var n int
				if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != 0 {
					t.Fatalf("partial canonical commit %s=%d %v", table, n, err)
				}
			}
			state, err := s.MCPProposal(t.Context(), "PROJECT-local", c.Occurrence)
			if err != nil || state.State != "admitted" {
				t.Fatalf("false receipt: %+v %v", state, err)
			}
			var decisions int
			if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM audit_events WHERE event_type='PERMISSION_DECIDED'").Scan(&decisions); err != nil || decisions != 0 {
				t.Fatalf("partial decision=%d %v", decisions, err)
			}
			if _, err := s.db.ExecContext(t.Context(), "DROP TRIGGER fail_memory"); err != nil {
				t.Fatal(err)
			}
			if err := s.CommitMCPMemoryDecision(t.Context(), "PROJECT-local", c.Occurrence, true, c.Record, memoryBrokerAudit()); err != nil {
				t.Fatal(err)
			}
			if err := s.CommitMCPMemoryDecision(t.Context(), "PROJECT-local", c.Occurrence, true, c.Record, memoryBrokerAudit()); err != nil {
				t.Fatal(err)
			}
			var n int
			if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM memory_outbox").Scan(&n); err != nil || n != 1 {
				t.Fatalf("duplicate outbox=%d %v", n, err)
			}
		})
	}
}
func TestMCPMemoryImmutableSnapshotIncludesConfidenceAndProvenance(t *testing.T) {
	for _, field := range []string{"confidence", "source_claims", "provider"} {
		t.Run(field, func(t *testing.T) {
			s, c := memoryBrokerFixture(t)
			rec := c.Record
			if field == "confidence" {
				rec.Confidence = model.ConfidenceVerified
			} else {
				rec.ExtMeta[field] = "forged"
			}
			if err := s.CommitMCPMemoryDecision(t.Context(), "PROJECT-local", c.Occurrence, true, rec, memoryBrokerAudit()); err == nil {
				t.Fatal("admitted snapshot tampering accepted")
			}
			if _, err := s.GetMemoryV2(t.Context(), "PROJECT-local", rec.ID); err == nil {
				t.Fatal("tampered snapshot persisted")
			}
		})
	}
}
func TestMCPMemorySourceFirewallRunsBeforeJournal(t *testing.T) {
	s, c := memoryBrokerFixture(t)
	rec := c.Record
	rec.ID = "MEM-source-secret"
	rec.ExtMeta = map[string]any{"source_claims": "api_key=sk-abcdefghijklmnop1234567890"}
	// Extreme never refuses the candidate: the firewall removes the secret
	// before anything is journaled, and the record names the redaction.
	admitted, _, err := s.AdmitMCPMemory(t.Context(), "PROJECT-local", MCPProposal{ID: "mcp:secret", Launch: "launch", Owner: "owner", Provider: "codex", Submission: "secret", Payload: `{"action":"memory","id":"MEM-source-secret"}`}, MCPMemoryCandidate{ImportKey: "secret", PayloadDigest: "secret-digest", Record: rec})
	if err != nil {
		t.Fatalf("firewall=%v", err)
	}
	if _, ok := admitted.Record.ExtMeta[security.RedactionMetaKey]; !ok {
		t.Fatalf("redaction evidence missing: %+v", admitted.Record.ExtMeta)
	}
	var journaled int
	if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM extreme_mcp_memory_candidates WHERE occurrence_id='mcp:secret' AND record_json LIKE '%sk-abcdefghijklmnop%'").Scan(&journaled); err != nil || journaled != 0 {
		t.Fatal("raw secret journaled")
	}
}
