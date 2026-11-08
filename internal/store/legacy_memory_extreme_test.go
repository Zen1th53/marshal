//go:build !governed

package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/statepath"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
)

func legacyFixture(t *testing.T, count int, target bool) (string, []model.MemoryRecordV2) {
	t.Helper()
	root, err := filepath.EvalSymlinks(testgit.New(t).Path())
	if err != nil {
		t.Fatal(err)
	}
	evidence := projectid.CollectEvidence(t.Context(), nil, root)
	evidence.CreatedNonce, err = projectid.NewNonce()
	if err != nil {
		t.Fatal(err)
	}
	id, _ := projectid.Derive(evidence)
	binding := projectid.Binding{ID: id, Evidence: evidence, RecordedRoot: root, SchemaVersion: 1}
	open := func(dir string) *Store {
		t.Helper()
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := projectid.SaveBinding(dir, binding); err != nil {
			t.Fatal(err)
		}
		s, err := Open(t.Context(), filepath.Join(dir, "state.db"))
		if err != nil {
			t.Fatal(err)
		}
		if err = s.Migrate(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err = s.InitProject(t.Context(), model.Project{ID: "PROJECT-local", Repository: root, DefaultBranch: "main", PackVersion: "fixture"}); err != nil {
			t.Fatal(err)
		}
		return s
	}
	s := open(filepath.Join(root, statepath.OtherDirName))
	if err = s.InitMCPProposals(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = s.InitMCPMemory(t.Context()); err != nil {
		t.Fatal(err)
	}
	var records []model.MemoryRecordV2
	for i := 0; i < count; i++ {
		now := time.Now().UTC()
		rec := model.MemoryRecordV2{ID: fmt.Sprintf("MEM-fixture-%02d", i), ProjectID: "PROJECT-local", Kind: model.MemoryKindSemantic, Lifecycle: model.MemoryCandidate, Confidence: model.ConfidenceInferred, Authority: model.AuthorityAgent, Title: fmt.Sprintf("private fixture %d", i), Body: fmt.Sprintf("durable curated source observation %d", i), Scope: "project", ScopeID: "PROJECT-local", ObservedAt: now, IngestedAt: now, ValidFrom: now, CreatedAt: now, UpdatedAt: now, Revision: 1, SessionID: "fixture-original-session", Source: model.MemorySource{Kind: "live_mcp", Reference: "fixture-owner"}, ExtMeta: map[string]any{"fixture_only": true, "launch_id": "fixture-launch", "provider": "codex", "canonical_project_root": root}}
		rec.ExtMeta["payload_digest"] = rec.CanonicalDigest()
		payload, _ := json.Marshal(map[string]string{"action": "memory", "id": rec.ID})
		occurrence := fmt.Sprintf("mcp:fixture-%02d", i)
		_, _, err = s.AdmitMCPMemory(t.Context(), "PROJECT-local", MCPProposal{ID: occurrence, Launch: "fixture-launch", Owner: "fixture-owner", Provider: "codex", Submission: rec.ID, Payload: string(payload)}, MCPMemoryCandidate{ImportKey: rec.ID, PayloadDigest: rec.CanonicalDigest(), Record: rec})
		if err != nil {
			t.Fatal(err)
		}
		audit := model.Event{ID: "EVENT-" + rec.ID, ProjectID: "PROJECT-local", Type: "PERMISSION_DECIDED", Timestamp: now, Data: map[string]any{"fixture_only": true, "allow": true}}
		if err = s.CommitMCPMemoryDecision(t.Context(), "PROJECT-local", occurrence, true, rec, audit); err != nil {
			t.Fatal(err)
		}
		if err = s.SetMCPProposalState(t.Context(), "PROJECT-local", occurrence, "applied", "fixture original applied receipt"); err != nil {
			t.Fatal(err)
		}
		rec, err = s.GetMemoryV2(t.Context(), "PROJECT-local", rec.ID)
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, rec)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	if target {
		if err = open(statepath.Path(root)).Close(); err != nil {
			t.Fatal(err)
		}
	}
	return root, records
}

func legacyFileHash(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest, _, _ := legacyDigest(string(raw))
	return digest
}

func TestExtremeLegacyMemoryThirteenPreservedWithoutAuthorityAndRollback(t *testing.T) {
	root, original := legacyFixture(t, 13, true)
	sourceHash := legacyFileHash(t, filepath.Join(root, statepath.OtherDirName, "state.db"))
	plan, err := PlanLegacyMemoryMigration(t.Context(), root, LegacyPrivateMemorySource)
	if err != nil || plan.Records != 13 || plan.Status != "DRY_RUN" {
		t.Fatal(plan, err)
	}
	got, err := ApplyLegacyMemoryMigration(t.Context(), root, LegacyPrivateMemorySource, plan.Digest)
	if err != nil || got.Status != "MIGRATED" {
		t.Fatal(got, err)
	}
	if sourceHash != legacyFileHash(t, filepath.Join(root, statepath.OtherDirName, "state.db")) {
		t.Fatal("source database changed")
	}
	info, err := os.Stat(got.BackupPath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("backup permissions", err)
	}
	var bundle legacyMemoryBundle
	raw, _ := os.ReadFile(got.BackupPath)
	if err = json.Unmarshal(raw, &bundle); err != nil || len(bundle.Facts) != 13 {
		t.Fatal("preservation bundle", err)
	}
	s, err := Open(t.Context(), statepath.Path(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, rec := range original {
		actual, err := s.GetMemoryV2(t.Context(), "PROJECT-local", rec.ID)
		if err != nil {
			t.Fatal(err)
		}
		wantDigest, _, _ := legacyDigest(rec)
		actualDigest, _, _ := legacyDigest(actual)
		if wantDigest != actualDigest {
			t.Fatal("record IDs/content/provenance/timestamps differ")
		}
		origin, err := s.MCPMemoryByRecordID(t.Context(), "PROJECT-local", rec.ID)
		if err != nil || origin.State != "historical_migration" {
			t.Fatal("original broker source classification was lost", origin.State, err)
		}
	}
	for _, table := range []string{"extreme_mcp_proposals", "extreme_mcp_memory_candidates"} {
		var exists int
		if err = s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&exists); err != nil || exists != 0 {
			t.Fatal("live broker authority transferred", table, err)
		}
	}
	for _, table := range []string{"agents", "tasks", "sessions", "capability_grants", "task_memory_grants"} {
		var exists int
		if err = s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists != 0 {
			var count int
			if err = s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
				t.Fatal("live state transferred", table, err)
			}
		}
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = ApplyLegacyMemoryMigration(t.Context(), root, LegacyPrivateMemorySource, plan.Digest); !errors.Is(err, model.ErrConflict) {
		t.Fatal("replay accepted", err)
	}
	rolled, err := RollbackLegacyMemoryMigration(t.Context(), root, plan.Digest)
	if err != nil || rolled.Records != 13 {
		t.Fatal(rolled, err)
	}
	s, err = Open(t.Context(), statepath.Path(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	list, err := s.ListMemoryV2(t.Context(), MemoryQueryFilter{ProjectID: "PROJECT-local"})
	if err != nil || len(list) != 0 {
		t.Fatal("rollback left canonical facts", len(list), err)
	}
	if sourceHash != legacyFileHash(t, filepath.Join(root, statepath.OtherDirName, "state.db")) {
		t.Fatal("rollback changed source")
	}
}

func TestExtremeLegacyMemoryDryRunDoesNotCreateTargetAndRejectsDrift(t *testing.T) {
	root, _ := legacyFixture(t, 1, false)
	plan, err := PlanLegacyMemoryMigration(t.Context(), root, LegacyPrivateMemorySource)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(statepath.Path(root)); !os.IsNotExist(err) {
		t.Fatal("dry-run initialized target")
	}
	if _, err = PlanLegacyMemoryMigration(t.Context(), root, "community"); !errors.Is(err, model.ErrInvalid) {
		t.Fatal("Community declared as private", err)
	}
	if _, err = ApplyLegacyMemoryMigration(t.Context(), root, LegacyPrivateMemorySource, "wrong"); !errors.Is(err, model.ErrConflict) {
		t.Fatal("digest drift accepted", plan.Digest, err)
	}
	s, err := Open(t.Context(), filepath.Join(root, statepath.OtherDirName, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(t.Context(), "UPDATE memory_records_v2 SET body='changed without valid original receipt' WHERE memory_id='MEM-fixture-00'"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err = ApplyLegacyMemoryMigration(t.Context(), root, LegacyPrivateMemorySource, plan.Digest); !errors.Is(err, model.ErrInvalid) && !errors.Is(err, model.ErrConflict) {
		t.Fatal("source drift accepted", err)
	}
}

func TestExtremeLegacyMemoryTargetBusyAndAtomicFault(t *testing.T) {
	root, _ := legacyFixture(t, 2, true)
	plan, err := PlanLegacyMemoryMigration(t.Context(), root, LegacyPrivateMemorySource)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(t.Context(), statepath.Path(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ApplyLegacyMemoryMigration(t.Context(), root, LegacyPrivateMemorySource, plan.Digest); !errors.Is(err, model.ErrConflict) {
		t.Fatal("live target accepted", err)
	}
	if _, err = s.db.ExecContext(t.Context(), `CREATE TRIGGER fixture_migration_fault BEFORE INSERT ON memory_records_v2 WHEN NEW.memory_id='MEM-fixture-01' BEGIN SELECT RAISE(ABORT,'fixture fault'); END`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err = ApplyLegacyMemoryMigration(t.Context(), root, LegacyPrivateMemorySource, plan.Digest); err == nil {
		t.Fatal("fault accepted")
	}
	s, err = Open(t.Context(), statepath.Path(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var count int
	if err = s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM memory_records_v2").Scan(&count); err != nil || count != 0 {
		t.Fatal("partial canonical import", count, err)
	}
	if err = s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM memory_outbox").Scan(&count); err != nil || count != 0 {
		t.Fatal("partial outbox import", count, err)
	}
}

func TestExtremeLegacyMemoryRollbackRejectsChangedRecords(t *testing.T) {
	root, _ := legacyFixture(t, 1, true)
	plan, err := PlanLegacyMemoryMigration(t.Context(), root, LegacyPrivateMemorySource)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ApplyLegacyMemoryMigration(t.Context(), root, LegacyPrivateMemorySource, plan.Digest); err != nil {
		t.Fatal(err)
	}
	s, err := Open(t.Context(), statepath.Path(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(t.Context(), "UPDATE memory_records_v2 SET revision=revision+1"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err = RollbackLegacyMemoryMigration(t.Context(), root, plan.Digest); !errors.Is(err, model.ErrConflict) {
		t.Fatal("changed memory rollback accepted", err)
	}
}

func TestExtremeLegacyMemoryRefusesSourceAliasesAndForeignRoot(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "foreign"} {
		t.Run(kind, func(t *testing.T) {
			root, _ := legacyFixture(t, 1, false)
			path := filepath.Join(root, statepath.OtherDirName, "state.db")
			switch kind {
			case "symlink":
				backup := path + ".real"
				if err := os.Rename(path, backup); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(backup, path); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(path, path+".alias"); err != nil {
					t.Fatal(err)
				}
			case "foreign":
				s, err := Open(t.Context(), path)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = s.db.ExecContext(t.Context(), "UPDATE projects SET repository='/synthetic/foreign/root'"); err != nil {
					t.Fatal(err)
				}
				s.Close()
			}
			if _, err := PlanLegacyMemoryMigration(t.Context(), root, LegacyPrivateMemorySource); !errors.Is(err, model.ErrInvalid) {
				t.Fatal("unsafe source accepted", err)
			}
		})
	}
}

func TestExtremeLegacyMemoryInitializationIsExclusiveAndStaleStageDoesNotBlock(t *testing.T) {
	root, _ := legacyFixture(t, 1, false)
	plan, err := PlanLegacyMemoryMigration(t.Context(), root, LegacyPrivateMemorySource)
	if err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(root, ".marshal-extreme-memory-init-fixture-interrupted")
	if err = os.Mkdir(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err = InitializeAndApplyLegacyMemoryMigration(t.Context(), root, LegacyPrivateMemorySource, "wrong"); !errors.Is(err, model.ErrConflict) {
		t.Fatal("unchecked initialization", err)
	}
	if _, err = os.Stat(statepath.Path(root)); !os.IsNotExist(err) {
		t.Fatal("bad digest created namespace")
	}
	if _, err = InitializeAndApplyLegacyMemoryMigration(t.Context(), root, LegacyPrivateMemorySource, plan.Digest); err != nil {
		t.Fatal("stale stage blocked legitimate retry", err)
	}
	if _, err = os.Stat(stale); err != nil {
		t.Fatal("unrelated old stage removed", err)
	}
	if _, err = InitializeAndApplyLegacyMemoryMigration(t.Context(), root, LegacyPrivateMemorySource, plan.Digest); !errors.Is(err, model.ErrConflict) {
		t.Fatal("published namespace overwritten", err)
	}
	stage := filepath.Join(root, "exclusive-fixture-stage")
	if err = os.Mkdir(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	if err = legacyPublishNamespace(stage, statepath.Path(root)); err == nil {
		t.Fatal("no-replace rename replaced existing namespace")
	}
	if _, err = os.Stat(stage); err != nil {
		t.Fatal("refused rename removed source stage")
	}
}

func TestExtremeLegacyMemoryRollbackRejectsLaterDependentMemory(t *testing.T) {
	root, records := legacyFixture(t, 1, true)
	plan, err := PlanLegacyMemoryMigration(t.Context(), root, LegacyPrivateMemorySource)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ApplyLegacyMemoryMigration(t.Context(), root, LegacyPrivateMemorySource, plan.Digest); err != nil {
		t.Fatal(err)
	}
	s, err := Open(t.Context(), statepath.Path(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	dependent := records[0]
	dependent.ID = "MEM-fixture-later-dependent"
	dependent.Source.Kind = "test"
	dependent.EvidenceIDs = []string{records[0].ID}
	if err = s.WriteMemoryV2(t.Context(), dependent); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err = RollbackLegacyMemoryMigration(t.Context(), root, plan.Digest); !errors.Is(err, model.ErrConflict) {
		t.Fatal("dependent memory rollback accepted", err)
	}
}

func TestExtremeLegacyMemoryRefusesForeignOriginalMetadataAndCorruptProposal(t *testing.T) {
	for _, kind := range []string{"metadata", "proposal"} {
		t.Run(kind, func(t *testing.T) {
			root, _ := legacyFixture(t, 1, false)
			s, err := Open(t.Context(), filepath.Join(root, statepath.OtherDirName, "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			if kind == "metadata" {
				var raw string
				if err = s.db.QueryRowContext(t.Context(), "SELECT record_json FROM extreme_mcp_memory_candidates").Scan(&raw); err != nil {
					t.Fatal(err)
				}
				var rec model.MemoryRecordV2
				if err = json.Unmarshal([]byte(raw), &rec); err != nil {
					t.Fatal(err)
				}
				rec.ExtMeta["canonical_project_root"] = "/synthetic/foreign/private/root"
				changed, _ := json.Marshal(rec)
				if _, err = s.db.ExecContext(t.Context(), "UPDATE extreme_mcp_memory_candidates SET record_json=?", string(changed)); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err = s.db.ExecContext(t.Context(), `UPDATE extreme_mcp_proposals SET payload='{"action":"memory","id":"MEM-forged"}'`); err != nil {
					t.Fatal(err)
				}
			}
			s.Close()
			if _, err = PlanLegacyMemoryMigration(t.Context(), root, LegacyPrivateMemorySource); !errors.Is(err, model.ErrInvalid) {
				t.Fatal("invalid original source provenance accepted", kind, err)
			}
		})
	}
}
