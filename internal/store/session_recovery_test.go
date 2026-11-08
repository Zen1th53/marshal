package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/model"
)

func recoveryFixture(t *testing.T) (*Store, model.SessionRecovery, CommandRecord) {
	t.Helper()
	s := openTestStore(t)
	ctx := context.Background()
	// Provisional v92 service fixture only. After I registers v94 this branch
	// disappears naturally; migration qualification must use ordinary Migrate.
	if LatestSchemaVersion < 94 {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err = migrateSessionRecovery(ctx, tx); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	g := model.GoalContract{ID: "recovery-goal", SessionID: "old-workspace", ProjectID: "recovery-project", Revision: 1, OriginalRequest: "repair the scratch fixture", RequestDigest: "original-request-digest", ConstitutionVersion: constitution.Current.String(), Confirmation: model.ConfirmationApproved, DesiredOutcome: "scratch fixture repaired", Risk: model.R1, AuthoritySource: "local-owner"}
	if err := s.SaveGoalContract(ctx, g, 0); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	grant := capability.Grant{ID: "recovery-grant", Subject: "local-owner", TaskID: capability.TaskID(g.ProjectID), Kind: capability.KindFilesystemWrite, Scope: capability.Scope{Resource: "scratch.db", Actions: []string{"session.recover"}}, Issuer: "local-owner", IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour), IdempotencyKey: "recovery-grant"}
	if err := s.PutCapabilityGrant(ctx, grant); err != nil {
		t.Fatal(err)
	}
	r := model.SessionRecovery{Version: 1, ID: "workspace-context", Generation: 1, SourceSessionID: g.SessionID, State: "recoverable", UpdatedAt: now, Binding: model.SessionRecoveryBinding{ProjectID: g.ProjectID, ProjectRoot: filepath.Clean(t.TempDir()), WorktreeDigest: "worktree", ConstitutionVersion: g.ConstitutionVersion, PolicyDigest: "none", GoalID: g.ID, GoalRevision: 1, OriginalIntentDigest: g.RequestDigest, GoalBudgetDigest: model.SessionRecoveryDigest(g.BudgetLimits), BudgetEvidenceDigest: "measured-budget"}}
	r.Digest = r.ContentDigest()
	c := CommandRecord{ProjectID: g.ProjectID, Actor: "local-owner", Key: "checkpoint-one", Operation: "session.recover", SessionID: g.SessionID, TargetID: r.ID, ExpectedVersion: 0, Digest: "checkpoint-command", CapabilityGrantID: string(grant.ID)}
	return s, r, c
}

func saveRecoveryFixture(t *testing.T, s *Store, r model.SessionRecovery, c CommandRecord) {
	t.Helper()
	if err := s.WriteSessionRecovery(WithCommand(context.Background(), c), r, 0, "", nil); err != nil {
		t.Fatal(err)
	}
}

func adoptRecovery(r model.SessionRecovery, c CommandRecord, key string) (model.SessionRecovery, CommandRecord) {
	old := r.Generation
	r.Generation++
	r.State = "adopted"
	r.AdoptedSessionID = "new-workspace-" + key
	r.UpdatedAt = time.Now().UTC()
	r.Digest = r.ContentDigest()
	c.ExpectedVersion = old
	c.Key = key
	c.Digest = "adopt-command-" + key
	return r, c
}

func TestSessionRecoverySurvivesRuntimeRestart(t *testing.T) {
	s, r, c := recoveryFixture(t)
	saveRecoveryFixture(t, s, r, c)
	var seq int
	var name, path string
	if err := s.db.QueryRow("PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.SessionRecovery(context.Background(), r.ID, 0)
	if err != nil || got.Digest != r.Digest || got.SourceSessionID != r.SourceSessionID {
		t.Fatalf("reopen: %+v %v", got, err)
	}
	next, c := adoptRecovery(r, c, "restart")
	if err = reopened.WriteSessionRecovery(WithCommand(context.Background(), c), next, r.Generation, r.Digest, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	proof, err := s.SessionRecovery(context.Background(), r.ID, 0)
	if err != nil || proof.State != "adopted" || proof.AdoptedSessionID != next.AdoptedSessionID {
		t.Fatalf("adoption readback: %+v %v", proof, err)
	}
	old, err := s.SessionRecovery(context.Background(), r.ID, r.Generation)
	if err != nil || old.State != "recoverable" {
		t.Fatal("original immutable context was overwritten")
	}
}

func TestSessionRecoveryConcurrentAdoptionCAS(t *testing.T) {
	s, r, c := recoveryFixture(t)
	saveRecoveryFixture(t, s, r, c)
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			next, command := adoptRecovery(r, c, fmt.Sprint(i))
			err := s.WriteSessionRecovery(WithCommand(context.Background(), command), next, r.Generation, r.Digest, func(context.Context) error { return nil })
			if err == nil {
				wins.Add(1)
			} else if !errors.Is(err, model.ErrConflict) {
				t.Errorf("adopt CAS: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("adoption winners=%d", wins.Load())
	}
}

func TestSessionRecoveryRejectsStaleGoalPolicyAndProject(t *testing.T) {
	for _, kind := range []string{"goal", "cancelled", "policy", "project", "root", "worktree", "budget", "missing-guard", "changed-digest"} {
		t.Run(kind, func(t *testing.T) {
			s, r, c := recoveryFixture(t)
			saveRecoveryFixture(t, s, r, c)
			next, command := adoptRecovery(r, c, kind)
			guard := func(context.Context) error { return nil }
			digest := r.Digest
			switch kind {
			case "goal", "cancelled":
				g, err := s.GetActiveGoalContract(context.Background(), r.SourceSessionID)
				if err != nil {
					t.Fatal(err)
				}
				g.Revision++
				if kind == "cancelled" {
					g.Confirmation = model.ConfirmationCancelled
				}
				if err = s.SaveGoalContract(context.Background(), g, 1); err != nil {
					t.Fatal(err)
				}
			case "policy":
				if _, err := s.db.Exec(`INSERT INTO policy_versions(policy_id,version,digest,generation,normalized_rules,status,state) VALUES('new-policy',1,'new-digest',1,'{}','active','active')`); err != nil {
					t.Fatal(err)
				}
			case "project":
				next.Binding.ProjectID = "foreign"
			case "root":
				next.Binding.ProjectRoot = filepath.Clean(t.TempDir())
			case "worktree":
				next.Binding.WorktreeDigest = "changed"
			case "budget":
				next.Binding.BudgetEvidenceDigest = "changed"
			case "missing-guard":
				guard = nil
			case "changed-digest":
				digest = "stale"
			}
			next.Digest = next.ContentDigest()
			if err := s.WriteSessionRecovery(WithCommand(context.Background(), command), next, r.Generation, digest, guard); err == nil {
				t.Fatal("stale or foreign context adopted")
			}
			proof, _ := s.SessionRecovery(context.Background(), r.ID, 0)
			if proof.State != "recoverable" || proof.Generation != 1 {
				t.Fatal("failed adoption mutated head")
			}
		})
	}
}

func TestSessionRecoveryRevalidatesMemory(t *testing.T) {
	for _, kind := range []string{"missing", "tombstone", "superseded", "changed-revision", "cycle"} {
		t.Run(kind, func(t *testing.T) {
			s, r, c := recoveryFixture(t)
			if err := s.InitProject(context.Background(), model.Project{ID: r.Binding.ProjectID, Repository: "scratch", DefaultBranch: "main", PackVersion: "1"}); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Add(-time.Minute)
			m := model.MemoryRecordV2{ID: "memory", ProjectID: r.Binding.ProjectID, Kind: model.MemoryKindSemantic, Lifecycle: model.MemoryCandidate, Confidence: model.ConfidenceInferred, Authority: model.AuthorityAgent, Title: "fixture", Body: "safe fixture", Scope: "project", ScopeID: r.Binding.ProjectID, Source: model.MemorySource{Kind: "runtime_service", Reference: "operator"}, ObservedAt: now, IngestedAt: now, ValidFrom: now, CreatedAt: now, UpdatedAt: now}
			if err := s.WriteMemoryV2(context.Background(), m); err != nil {
				t.Fatal(err)
			}
			m, err := s.GetMemoryV2(context.Background(), r.Binding.ProjectID, m.ID)
			if err != nil {
				t.Fatal(err)
			}
			r.Memory = []model.SessionRecoveryMemoryRef{{ID: m.ID, Revision: m.Revision, Digest: m.ContentDigest}}
			r.Digest = r.ContentDigest()
			saveRecoveryFixture(t, s, r, c)
			switch kind {
			case "missing":
				s.db.Exec(`DELETE FROM memory_records_v2 WHERE memory_id='memory'`)
			case "tombstone":
				s.db.Exec(`UPDATE memory_records_v2 SET lifecycle='tombstoned' WHERE memory_id='memory'`)
			case "superseded", "cycle":
				s.db.Exec(`UPDATE memory_records_v2 SET lifecycle='superseded',superseded_by='["memory"]' WHERE memory_id='memory'`)
			case "changed-revision":
				s.db.Exec(`UPDATE memory_records_v2 SET revision=revision+1 WHERE memory_id='memory'`)
			}
			next, command := adoptRecovery(r, c, kind)
			if err = s.WriteSessionRecovery(WithCommand(context.Background(), command), next, r.Generation, r.Digest, func(context.Context) error { return nil }); err == nil {
				t.Fatal("inapplicable memory adopted")
			}
		})
	}
}

func TestSessionRecoveryNeverReplaysConfirmationOrProviderTurn(t *testing.T) {
	s, r, c := recoveryFixture(t)
	saveRecoveryFixture(t, s, r, c)
	next, command := adoptRecovery(r, c, "safe")
	if err := s.WriteSessionRecovery(WithCommand(context.Background(), command), next, 1, r.Digest, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(next)
	for _, forbidden := range []string{"confirmation", "approval_id", "transcript", "provider_turn", "private_key", "lease", "state_snapshot"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("recovery persisted %s", forbidden)
		}
	}
	for _, table := range []string{"worker_runs", "tasks"} {
		var count int
		if err := s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("adoption dispatched %s: %d %v", table, count, err)
		}
	}
}

func TestSessionRecoveryAuditFailureAndRevocationRollBack(t *testing.T) {
	for _, kind := range []string{"audit", "revocation", "guard"} {
		t.Run(kind, func(t *testing.T) {
			s, r, c := recoveryFixture(t)
			saveRecoveryFixture(t, s, r, c)
			next, command := adoptRecovery(r, c, kind)
			guard := func(context.Context) error { return nil }
			switch kind {
			case "audit":
				if _, err := s.db.Exec(`CREATE TRIGGER refuse_recovery_audit BEFORE INSERT ON command_audit BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
					t.Fatal(err)
				}
			case "revocation":
				if err := s.RevokeCapabilityGrant(context.Background(), capability.GrantID(c.CapabilityGrantID), time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
			case "guard":
				guard = func(context.Context) error { return model.ErrConflict }
			}
			if err := s.WriteSessionRecovery(WithCommand(context.Background(), command), next, 1, r.Digest, guard); err == nil {
				t.Fatal("unsafe adoption committed")
			}
			proof, _ := s.SessionRecovery(context.Background(), r.ID, 0)
			if proof.Generation != 1 {
				t.Fatal("rollback lost original head")
			}
			if _, found, err := s.CommandResult(context.Background(), command); err != nil || found {
				t.Fatal("failed adoption left a receipt")
			}
		})
	}
}

func TestSessionRecoveryMigrationRollbackAndPreservation(t *testing.T) {
	if LatestSchemaVersion >= 94 {
		t.Skip("registered migration rollback/upgrade matrix belongs to I's migration window")
	}
	s := openTestStore(t)
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = migrateSessionRecovery(ctx, tx); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name LIKE 'session_recovery_%'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rolled-back DDL survived: %d %v", count, err)
	}
	var version int
	if err = s.db.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&version); err != nil || version != LatestSchemaVersion {
		t.Fatalf("helper independently advanced schema: %d %v", version, err)
	}
}
