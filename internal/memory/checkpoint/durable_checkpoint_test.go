package checkpoint_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/checkpoint"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

func setupTestStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	ctx := context.Background()
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "marshal_durable_ckpt_test.db")

	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("st.Migrate: %v", err)
	}
	return st, dbPath
}

// Test1_ProcessCrashAfterCheckpointBeforeHandoffConsume:
// Process crash after checkpoint before handoff consume preserves checkpoint for recovery.
func Test1_ProcessCrashAfterCheckpointBeforeHandoffConsume(t *testing.T) {
	ctx := context.Background()
	st, dbPath := setupTestStore(t)

	mgr := checkpoint.NewDurableCheckpointManager(st)

	cp := model.HandoffCheckpoint{
		ID:                "CKPT-CRASH-PRE-CONSUME",
		Version:           1,
		GoalID:            "GOAL-1",
		GoalRevision:      1,
		ConstraintsDigest: "sha256:abc123456",
		TaskID:            "TASK-1",
		SessionID:         "SESS-1",
		HandoffID:         "HO-1",
		Author: model.AuthorProvenance{
			AgentID: "codex-core",
			Harness: "codex-cli",
		},
		Role:      "developer",
		TaskSlots: map[string]string{"phase": "implementation", "status": "checkpointed"},
		ClaimIDs:  []string{"CLAIM-1"},
		CreatedAt: time.Now().UTC().Truncate(time.Millisecond),
	}

	if err := mgr.CreateHandoffCheckpoint(ctx, cp); err != nil {
		t.Fatalf("CreateHandoffCheckpoint: %v", err)
	}

	// Simulate sudden process termination
	_ = st.Close()

	// Restart process
	st2, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer st2.Close()

	recovered, err := st2.GetHandoffCheckpoint(ctx, cp.ID)
	if err != nil {
		t.Fatalf("GetHandoffCheckpoint after crash: %v", err)
	}

	if recovered.ID != cp.ID || recovered.TaskSlots["phase"] != "implementation" {
		t.Fatalf("recovered checkpoint mismatch: %+v", recovered)
	}
}

// Test2_CrashAfterRecipientAcceptsBeforeFirstWrite:
// Recipient accepts handoff, then crashes before performing writes.
// Checkpoint preserves pre-write state for restart recovery.
func Test2_CrashAfterRecipientAcceptsBeforeFirstWrite(t *testing.T) {
	ctx := context.Background()
	st, dbPath := setupTestStore(t)

	mgr := checkpoint.NewDurableCheckpointManager(st)

	cpPreWrite := model.HandoffCheckpoint{
		ID:                "CKPT-PRE-WRITE",
		Version:           1,
		GoalID:            "GOAL-1",
		GoalRevision:      1,
		ConstraintsDigest: "sha256:digest-pre-write",
		TaskID:            "TASK-2",
		SessionID:         "SESS-2",
		Author: model.AuthorProvenance{
			AgentID: "claude-arch",
			Harness: "claude",
		},
		Role:      "architect",
		TaskSlots: map[string]string{"clean_state": "true", "write_count": "0"},
		CreatedAt: time.Now().UTC().Truncate(time.Millisecond),
	}

	if err := mgr.CreateHandoffCheckpoint(ctx, cpPreWrite); err != nil {
		t.Fatalf("CreateHandoffCheckpoint: %v", err)
	}

	// Simulate crash
	_ = st.Close()

	// Restart
	st2, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer st2.Close()

	latest, err := st2.GetLatestHandoffCheckpoint(ctx, "TASK-2")
	if err != nil {
		t.Fatalf("GetLatestHandoffCheckpoint: %v", err)
	}

	if latest.TaskSlots["write_count"] != "0" {
		t.Fatalf("expected pre-write state preserved, got: %v", latest.TaskSlots)
	}
}

// Test3_RestartAndResumeSameProvider:
// Codex starts task, saves checkpoint, process restarts, resumes same provider.
func Test3_RestartAndResumeSameProvider(t *testing.T) {
	ctx := context.Background()
	st, dbPath := setupTestStore(t)

	mgr := checkpoint.NewDurableCheckpointManager(st)

	cp := model.HandoffCheckpoint{
		ID:           "CKPT-SAME-PROVIDER",
		GoalID:       "GOAL-1",
		GoalRevision: 1,
		TaskID:       "TASK-SAME",
		SessionID:    "SESS-CODEX",
		Author: model.AuthorProvenance{
			AgentID: "codex-core",
			Harness: "codex-cli",
		},
		Role:      "developer",
		TaskSlots: map[string]string{"current_step": "build_adapter"},
		CreatedAt: time.Now().UTC().Truncate(time.Millisecond),
	}

	if err := mgr.CreateHandoffCheckpoint(ctx, cp); err != nil {
		t.Fatalf("CreateHandoffCheckpoint: %v", err)
	}
	_ = st.Close()

	// Restart and resume with same provider
	st2, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer st2.Close()

	resumed, err := st2.GetHandoffCheckpoint(ctx, cp.ID)
	if err != nil {
		t.Fatalf("GetHandoffCheckpoint: %v", err)
	}
	if resumed.Author.Harness != "codex-cli" || resumed.TaskSlots["current_step"] != "build_adapter" {
		t.Fatalf("resumed checkpoint corrupted: %+v", resumed)
	}
}

// Test4_RestartAndResumeDifferentProvider:
// Checkpoint saved by Codex; process restarts; Claude or Antigravity resumes seamlessly.
func Test4_RestartAndResumeDifferentProvider(t *testing.T) {
	ctx := context.Background()
	st, dbPath := setupTestStore(t)

	mgr := checkpoint.NewDurableCheckpointManager(st)

	cpCodex := model.HandoffCheckpoint{
		ID:                "CKPT-CODEX-OUT",
		GoalID:            "GOAL-1",
		GoalRevision:      1,
		ConstraintsDigest: "sha256:constraint-1",
		TaskID:            "TASK-DIFF-PROV",
		SessionID:         "SESS-1",
		HandoffID:         "HO-CODEX-TO-ANTIGRAV",
		Author: model.AuthorProvenance{
			AgentID: "codex-core",
			Harness: "codex-cli",
		},
		Role:      "developer",
		TaskSlots: map[string]string{"artifact": "internal/auth/token.go"},
		CreatedAt: time.Now().UTC().Truncate(time.Millisecond),
	}

	if err := mgr.CreateHandoffCheckpoint(ctx, cpCodex); err != nil {
		t.Fatalf("CreateHandoffCheckpoint: %v", err)
	}
	_ = st.Close()

	// Antigravity resumes on restarted system
	st2, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer st2.Close()

	mgr2 := checkpoint.NewDurableCheckpointManager(st2)

	loaded, err := st2.GetHandoffCheckpoint(ctx, cpCodex.ID)
	if err != nil {
		t.Fatalf("GetHandoffCheckpoint: %v", err)
	}

	// Antigravity records its next checkpoint continuing the work
	cpAntigrav := model.HandoffCheckpoint{
		ID:                "CKPT-ANTIGRAV-IN",
		GoalID:            loaded.GoalID,
		GoalRevision:      loaded.GoalRevision,
		ConstraintsDigest: loaded.ConstraintsDigest,
		TaskID:            loaded.TaskID,
		SessionID:         "SESS-ANTIGRAV",
		Author: model.AuthorProvenance{
			AgentID: "antigravity-int",
			Harness: "antigravity",
		},
		Role:      "developer",
		TaskSlots: map[string]string{"artifact": loaded.TaskSlots["artifact"], "verified_by": "antigravity"},
		CreatedAt: time.Now().UTC().Truncate(time.Millisecond),
	}

	if err := mgr2.CreateHandoffCheckpoint(ctx, cpAntigrav); err != nil {
		t.Fatalf("Antigravity CreateHandoffCheckpoint: %v", err)
	}

	history, err := st2.ListHandoffCheckpoints(ctx, "TASK-DIFF-PROV")
	if err != nil {
		t.Fatalf("ListHandoffCheckpoints: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("expected 2 checkpoints in history, got %d", len(history))
	}
	if history[0].Author.Harness != "codex-cli" || history[1].Author.Harness != "antigravity" {
		t.Fatalf("checkpoint history provider lineage mismatch: %s -> %s",
			history[0].Author.Harness, history[1].Author.Harness)
	}
}

// Test5_RollbackInvalidatesLaterClaimsPreservesEvidence:
// Rollback to Checkpoint 1 invalidates later dependent claims while preserving historical evidence.
func Test5_RollbackInvalidatesLaterClaimsPreservesEvidence(t *testing.T) {
	ctx := context.Background()
	st, _ := setupTestStore(t)
	defer st.Close()

	mgr := checkpoint.NewDurableCheckpointManager(st)

	now := time.Now().UTC().Truncate(time.Millisecond)

	// Checkpoint 1
	cp1 := model.HandoffCheckpoint{
		ID:           "CKPT-1",
		GoalID:       "GOAL-1",
		GoalRevision: 1,
		TaskID:       "TASK-ROLLBACK",
		SessionID:    "SESS-1",
		Author:       model.AuthorProvenance{AgentID: "codex-1"},
		Role:         "developer",
		TaskSlots:    map[string]string{"step": "1"},
		ClaimIDs:     []string{"CLAIM-1"},
		EvidenceIDs:  []string{"EVID-1"},
		CreatedAt:    now,
	}
	if err := mgr.CreateHandoffCheckpoint(ctx, cp1); err != nil {
		t.Fatalf("CreateHandoffCheckpoint cp1: %v", err)
	}

	// Claim 1 created at Checkpoint 1
	claim1 := model.Claim{
		ID:             "CLAIM-1",
		GoalID:         "GOAL-1",
		GoalRevision:   1,
		Subject:        "core.step1",
		NormalizedText: "Step 1 complete",
		Scope:          "core",
		Criticality:    model.CriticalityBlocker,
		State:          model.ClaimStateVerified,
		Author:         model.AuthorProvenance{AgentID: "codex-1"},
		CreatedAt:      now,
	}
	if err := st.SaveClaim(ctx, claim1); err != nil {
		t.Fatalf("SaveClaim 1: %v", err)
	}

	// Work advances: Checkpoint 2 and Claim 2
	later := now.Add(time.Hour)
	cp2 := model.HandoffCheckpoint{
		ID:           "CKPT-2",
		GoalID:       "GOAL-1",
		GoalRevision: 1,
		TaskID:       "TASK-ROLLBACK",
		SessionID:    "SESS-1",
		Author:       model.AuthorProvenance{AgentID: "codex-1"},
		Role:         "developer",
		TaskSlots:    map[string]string{"step": "2"},
		ClaimIDs:     []string{"CLAIM-1", "CLAIM-2"},
		EvidenceIDs:  []string{"EVID-1", "EVID-2"},
		CreatedAt:    later,
	}
	if err := mgr.CreateHandoffCheckpoint(ctx, cp2); err != nil {
		t.Fatalf("CreateHandoffCheckpoint cp2: %v", err)
	}

	claim2 := model.Claim{
		ID:             "CLAIM-2",
		GoalID:         "GOAL-1",
		GoalRevision:   1,
		Subject:        "core.step2",
		NormalizedText: "Step 2 failed downstream",
		Scope:          "core",
		Criticality:    model.CriticalityBlocker,
		State:          model.ClaimStateVerified,
		Author:         model.AuthorProvenance{AgentID: "codex-1"},
		CreatedAt:      later,
	}
	if err := st.SaveClaim(ctx, claim2); err != nil {
		t.Fatalf("SaveClaim 2: %v", err)
	}

	// Execute Rollback to Checkpoint 1
	res, err := mgr.RollbackToCheckpoint(ctx, "CKPT-1", model.AuthorProvenance{AgentID: "operator"}, "Rollback downstream failure")
	if err != nil {
		t.Fatalf("RollbackToCheckpoint: %v", err)
	}

	// Checkpoint 1 task slots restored
	if res.RestoredTaskSlots["step"] != "1" {
		t.Fatalf("restored task slots = %v, want step=1", res.RestoredTaskSlots)
	}

	// Claim 2 was invalidated
	c2Loaded, err := st.GetClaim(ctx, "CLAIM-2")
	if err != nil {
		t.Fatalf("GetClaim 2: %v", err)
	}
	if c2Loaded.State != model.ClaimStateInvalidated {
		t.Fatalf("Claim 2 state after rollback = %s, want INVALIDATED", c2Loaded.State)
	}

	// Claim 1 is preserved as VERIFIED
	c1Loaded, err := st.GetClaim(ctx, "CLAIM-1")
	if err != nil {
		t.Fatalf("GetClaim 1: %v", err)
	}
	if c1Loaded.State != model.ClaimStateVerified {
		t.Fatalf("Claim 1 state after rollback = %s, want VERIFIED", c1Loaded.State)
	}

	// Checkpoint 1 evidence IDs preserved
	if len(res.PreservedEvidenceIDs) != 1 || res.PreservedEvidenceIDs[0] != "EVID-1" {
		t.Fatalf("expected historical evidence EVID-1 preserved, got: %v", res.PreservedEvidenceIDs)
	}
}

// Test6_RollbackNeverDeletesUnrelatedHostChanges:
// Rollback to checkpoint restores MARSHAL task state but never touches unrelated user files.
func Test6_RollbackNeverDeletesUnrelatedHostChanges(t *testing.T) {
	ctx := context.Background()
	st, _ := setupTestStore(t)
	defer st.Close()

	mgr := checkpoint.NewDurableCheckpointManager(st)

	// Create an unrelated user file on disk
	userDir := t.TempDir()
	userFile := filepath.Join(userDir, "user_personal_notes.txt")
	if err := os.WriteFile(userFile, []byte("valuable uncommitted user work"), 0644); err != nil {
		t.Fatalf("create user file: %v", err)
	}

	cp := model.HandoffCheckpoint{
		ID:           "CKPT-HOST-TEST",
		GoalID:       "GOAL-1",
		GoalRevision: 1,
		TaskID:       "TASK-HOST",
		SessionID:    "SESS-1",
		Author:       model.AuthorProvenance{AgentID: "codex-1"},
		Role:         "developer",
		CreatedAt:    time.Now().UTC(),
	}
	if err := mgr.CreateHandoffCheckpoint(ctx, cp); err != nil {
		t.Fatalf("CreateHandoffCheckpoint: %v", err)
	}

	// Execute rollback
	_, err := mgr.RollbackToCheckpoint(ctx, cp.ID, model.AuthorProvenance{AgentID: "operator"}, "test host isolation")
	if err != nil {
		t.Fatalf("RollbackToCheckpoint: %v", err)
	}

	// Unrelated user file MUST still exist and have unchanged contents
	content, err := os.ReadFile(userFile)
	if err != nil {
		t.Fatalf("user file was deleted during rollback: %v", err)
	}
	if string(content) != "valuable uncommitted user work" {
		t.Fatalf("user file corrupted during rollback")
	}
}

// Test7_ConcurrentCheckpointAttemptsAreIdempotent:
// Concurrent attempts to save the same checkpoint ID succeed idempotently without race conditions.
func Test7_ConcurrentCheckpointAttemptsAreIdempotent(t *testing.T) {
	ctx := context.Background()
	st, _ := setupTestStore(t)
	defer st.Close()

	mgr := checkpoint.NewDurableCheckpointManager(st)

	cp := model.HandoffCheckpoint{
		ID:           "CKPT-CONCURRENT-IDEMP",
		GoalID:       "GOAL-1",
		GoalRevision: 1,
		TaskID:       "TASK-CONC",
		SessionID:    "SESS-CONC",
		Author:       model.AuthorProvenance{AgentID: "codex-1"},
		Role:         "developer",
		TaskSlots:    map[string]string{"thread": "concurrency_test"},
		CreatedAt:    time.Now().UTC().Truncate(time.Millisecond),
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := mgr.CreateHandoffCheckpoint(ctx, cp); err != nil {
				errCh <- err
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("concurrent save checkpoint failed: %v", err)
	}

	loaded, err := st.GetHandoffCheckpoint(ctx, cp.ID)
	if err != nil {
		t.Fatalf("GetHandoffCheckpoint: %v", err)
	}
	if loaded.ID != cp.ID {
		t.Fatalf("loaded checkpoint ID mismatch")
	}
}

// Test8_HandoffCheckpointSurvivesDBReopenBackupRestore:
// Handoff checkpoint survives DB close/reopen and backup/restore verification.
func Test8_HandoffCheckpointSurvivesDBReopenBackupRestore(t *testing.T) {
	ctx := context.Background()
	st, _ := setupTestStore(t)

	mgr := checkpoint.NewDurableCheckpointManager(st)

	now := time.Now().UTC().Truncate(time.Millisecond)
	cp := model.HandoffCheckpoint{
		ID:                "CKPT-BACKUP-TEST",
		Version:           1,
		GoalID:            "GOAL-BACKUP",
		GoalRevision:      1,
		ConstraintsDigest: "sha256:backupdigest12345",
		TaskID:            "TASK-BACKUP",
		SessionID:         "SESS-BACKUP",
		HandoffID:         "HO-BACKUP",
		Author: model.AuthorProvenance{
			AgentID: "codex-1",
			Harness: "codex-cli",
		},
		Role:        "developer",
		Branch:      "feat/v1.5-dev",
		TaskSlots:   map[string]string{"backup_ready": "true"},
		ClaimIDs:    []string{"CLAIM-B1"},
		EvidenceIDs: []string{"EVID-B1"},
		CreatedAt:   now,
	}

	if err := mgr.CreateHandoffCheckpoint(ctx, cp); err != nil {
		t.Fatalf("CreateHandoffCheckpoint: %v", err)
	}

	// Initialize project and backup DB
	if err := st.InitProject(ctx, model.Project{
		ID:            "PRJ-BACKUP-TEST",
		Repository:    "repo/test",
		DefaultBranch: "main",
		PackVersion:   "1.5.0",
	}); err != nil {
		t.Fatalf("InitProject: %v", err)
	}

	backupPath := filepath.Join(t.TempDir(), "backup.tar.gz")
	meta, err := st.Backup(ctx, backupPath)
	if err != nil {
		t.Fatalf("st.Backup: %v", err)
	}
	if meta.SchemaVersion != store.LatestSchemaVersion {
		t.Fatalf("backup schema version = %d, want %d", meta.SchemaVersion, store.LatestSchemaVersion)
	}

	// Close original DB
	_ = st.Close()

	// Restore DB to new path
	restoreDir := t.TempDir()
	restorePath := filepath.Join(restoreDir, "restored.db")
	if err := store.RestoreDatabase(ctx, backupPath, restorePath, "PRJ-BACKUP-TEST", store.LatestSchemaVersion); err != nil {
		t.Fatalf("RestoreDatabase: %v", err)
	}

	// Open restored DB
	stRestored, err := store.Open(ctx, restorePath)
	if err != nil {
		t.Fatalf("open restored store: %v", err)
	}
	defer stRestored.Close()

	loaded, err := stRestored.GetHandoffCheckpoint(ctx, cp.ID)
	if err != nil {
		t.Fatalf("GetHandoffCheckpoint from restored DB: %v", err)
	}

	if loaded.ID != cp.ID || loaded.GoalID != cp.GoalID || loaded.ConstraintsDigest != cp.ConstraintsDigest {
		t.Fatalf("restored checkpoint mismatch: %+v", loaded)
	}
	if loaded.TaskSlots["backup_ready"] != "true" {
		t.Fatalf("restored checkpoint slots corrupted: %+v", loaded.TaskSlots)
	}
}
