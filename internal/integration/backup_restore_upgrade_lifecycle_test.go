package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/evidence"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/netpolicy"
	"github.com/Zen1th53/marshal/internal/store"
)

func TestBackupRestoreUpgradeLifecycleE2E(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "original.db")
	backupPath := filepath.Join(dir, "backup.db")
	restoredPath := filepath.Join(dir, "restored.db")

	const projectID = "PRJ-LIFECYCLE"

	// 1. Initialize original database and migrate to latest schema
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("st.Migrate: %v", err)
	}
	if err := st.InitProject(ctx, model.Project{
		ID:            projectID,
		Repository:    dir,
		DefaultBranch: "main",
		PackVersion:   "1.0.0",
	}); err != nil {
		t.Fatalf("InitProject: %v", err)
	}

	// 2. Populate representative state
	task := model.Task{
		ID:     "TASK-LIFECYCLE-001",
		Title:  "Lifecycle Verification Task",
		Status: model.TaskReady,
		Risk:   model.R1,
	}
	if _, err := st.ImportTasks(ctx, []model.Task{task}); err != nil {
		t.Fatalf("ImportTasks: %v", err)
	}

	agent := model.Agent{
		ID:          "AGT-001",
		ProjectID:   projectID,
		DisplayName: "Lifecycle Agent",
		Role:        model.RoleDeveloper,
		Status:      model.AgentActive,
		CreatedAt:   time.Now().UTC(),
	}
	if err := st.RegisterAgent(ctx, agent); err != nil {
		t.Fatalf("RegisterAgent: %v", err)
	}

	now := time.Now().UTC()
	mem := model.MemoryRecordV2{
		ID:         "MEM-001",
		ProjectID:  projectID,
		Kind:       model.MemoryKindDecision,
		Lifecycle:  model.MemoryDurable,
		Confidence: model.ConfidenceVerified,
		Authority:  model.AuthorityOperator,
		Title:      "Primary Runtime Configuration",
		Body:       "runtime uses sqlite with WAL mode",
		Scope:      string(model.ScopeProject),
		ScopeID:    projectID,
		ObservedAt: now,
		IngestedAt: now,
		ValidFrom:  now,
		CreatedAt:  now,
		UpdatedAt:  now,
		Source:     model.MemorySource{Kind: "test"},
	}
	if err := st.WriteMemoryV2(ctx, mem); err != nil {
		t.Fatalf("WriteMemoryV2: %v", err)
	}

	metaMap := map[string]string{"path": "build/artifact.tar.gz"}
	digest, err := evidence.CanonicalDigest(evidence.NodeTypeArtifact, metaMap)
	if err != nil {
		t.Fatalf("CanonicalDigest: %v", err)
	}

	evNode := evidence.Node{
		ID:        "node-001",
		Type:      evidence.NodeTypeArtifact,
		State:     evidence.StateStored,
		Digest:    digest,
		CreatedAt: now,
		Metadata:  metaMap,
	}
	if _, err := st.PutNode(ctx, evNode); err != nil {
		t.Fatalf("PutNode: %v", err)
	}

	egressDec := netpolicy.DecisionRecord{
		ID:             "DEC-001",
		IdempotencyKey: "idem-001",
		Request: netpolicy.Request{
			SubjectID: "AGT-001",
			TaskID:    task.ID,
			Host:      "api.github.com",
			Protocol:  netpolicy.ProtocolTCP,
			Port:      443,
		},
		Decision: netpolicy.Decision{
			Allowed: true,
			RuleID:  "rule-github",
			Reason:  netpolicy.ReasonAllowed,
			Host:    "api.github.com",
			Port:    443,
		},
		CreatedAt: time.Now().UTC(),
	}
	if err := st.PutEgressDecision(ctx, egressDec); err != nil {
		t.Fatalf("PutEgressDecision: %v", err)
	}

	// 3. Perform atomic backup
	if _, err := st.Backup(ctx, backupPath); err != nil {
		t.Fatalf("st.Backup: %v", err)
	}
	_ = st.Close()

	// 4. Verify backup artifact integrity
	meta, err := store.VerifyBackup(ctx, backupPath, projectID, store.LatestSchemaVersion)
	if err != nil {
		t.Fatalf("store.VerifyBackup failed: %v", err)
	}
	if meta.ProjectID != projectID || meta.SchemaVersion != store.LatestSchemaVersion {
		t.Fatalf("unexpected backup metadata: %+v", meta)
	}

	// 5. Corrupt or delete original database to simulate catastrophe
	if err := os.Remove(dbPath); err != nil && !os.IsNotExist(err) {
		t.Fatalf("remove original db: %v", err)
	}

	// 6. Restore database from backup
	if err := store.RestoreDatabase(ctx, backupPath, restoredPath, projectID, store.LatestSchemaVersion); err != nil {
		t.Fatalf("store.RestoreDatabase failed: %v", err)
	}

	// 7. Open restored database and verify data integrity
	restoredStore, err := store.Open(ctx, restoredPath)
	if err != nil {
		t.Fatalf("store.Open restored db: %v", err)
	}
	defer restoredStore.Close()

	// Verify Task
	restoredTasks, err := restoredStore.ListTasks(ctx)
	if err != nil {
		t.Fatalf("ListTasks restored: %v", err)
	}
	if len(restoredTasks) != 1 || restoredTasks[0].ID != task.ID {
		t.Fatalf("restored tasks mismatch: got %+v, want %+v", restoredTasks, task)
	}

	// Verify Memory
	restoredMem, err := restoredStore.GetMemoryV2(ctx, projectID, mem.ID)
	if err != nil {
		t.Fatalf("GetMemoryV2 restored: %v", err)
	}
	if restoredMem.Body != mem.Body || restoredMem.Kind != mem.Kind {
		t.Fatalf("restored memory mismatch: got %+v, want %+v", restoredMem, mem)
	}

	// Verify Evidence
	restoredEv, err := restoredStore.Get(ctx, evNode.ID)
	if err != nil {
		t.Fatalf("Get restored evidence: %v", err)
	}
	if restoredEv.Digest != evNode.Digest {
		t.Fatalf("restored evidence mismatch: got %+v, want %+v", restoredEv, evNode)
	}

	// 8. Re-run migration to test schema upgrade idempotency
	if err := restoredStore.Migrate(ctx); err != nil {
		t.Fatalf("restoredStore.Migrate idempotency failure: %v", err)
	}

	// 9. Run final PRAGMA integrity check on restored and migrated database
	finalMeta, err := store.VerifyBackup(ctx, restoredPath, projectID, store.LatestSchemaVersion)
	if err != nil {
		t.Fatalf("final integrity verification failed: %v", err)
	}
	if finalMeta.SchemaVersion != store.LatestSchemaVersion {
		t.Fatalf("expected schema %d, got %d", store.LatestSchemaVersion, finalMeta.SchemaVersion)
	}
}
