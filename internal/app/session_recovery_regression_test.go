package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/Zen1th53/marshal/internal/statepath"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

func TestSessionRecoveryAdoptionValidatesSourceAndVerifiesProcessLiveness_Regression(t *testing.T) {
	if store.LatestSchemaVersion < 94 {
		t.Skip("integration prerequisite: v94 recovery required")
	}
	r := runtimeForPlan(t)
	ctx := context.Background()
	local, err := r.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ctx = local.Context(ctx)

	const sourceSession = "SESSION-source-producer"
	const destinationSession = "SESSION-dest-adopter"

	// 1. Setup source session goal
	g, err := r.store.GetActiveGoalContract(ctx, "SESSION-plan")
	if err != nil {
		t.Fatal(err)
	}
	g.ID = "GOAL-source-test"
	g.SessionID = sourceSession
	g.ProjectID = r.ProjectIdentity()
	g.RequestDigest = model.SessionRecoveryDigest(g.OriginalRequest)
	g.Revision = 1
	if err = r.store.SaveGoalContract(ctx, g, 0); err != nil {
		t.Fatal(err)
	}

	b := model.SessionRecoveryBinding{
		ProjectID:            r.ProjectIdentity(),
		ProjectRoot:          r.ProjectRoot(),
		WorktreeDigest:       "fixture-worktree",
		ConstitutionVersion:  constitution.Current.String(),
		PolicyDigest:         "none",
		GoalID:               g.ID,
		GoalRevision:         g.Revision,
		OriginalIntentDigest: g.RequestDigest,
		GoalBudgetDigest:     model.SessionRecoveryDigest(g.BudgetLimits),
		BudgetEvidenceDigest: "fixture-budget",
	}

	// Record source session as LIVE
	RecordSessionLiveness(r.ProjectRoot(), sourceSession)
	defer ClearSessionLiveness(r.ProjectRoot(), sourceSession)

	var mu sync.Mutex
	// The observer observes the checkpoint record 'rec'. It validates source process liveness.
	hooks := SessionRecoveryHooks{
		Freeze: func(context.Context) (func(), error) {
			mu.Lock()
			return mu.Unlock, nil
		},
		Observe: func(ctx context.Context, rec model.SessionRecovery) (model.SessionRecoveryBinding, MemoryReconcileRequest, bool, error) {
			// Defect #7 regression: if observe uses destination session instead of sourceSession from rec,
			// or assumes sourceStopped=true unconditionally.
			source := rec.SourceSessionID
			if source == "" {
				source = sourceSession
			}
			stopped := VerifySourceProcessStopped(r.ProjectRoot(), source, destinationSession)
			return b, MemoryReconcileRequest{ProjectID: b.ProjectID}, stopped, nil
		},
	}

	service := r.WorkspaceRecovery(hooks)
	e := CommandEnvelope{
		ProjectID:      b.ProjectID,
		SessionID:      sourceSession,
		TargetID:       "recovery-source-test",
		IdempotencyKey: "checkpoint-source",
	}
	checkpoint, err := service.Checkpoint(ctx, e, b, nil, "")
	if err != nil {
		t.Fatalf("checkpoint failed: %v", err)
	}

	// 2. Attempt adoption while source session is still LIVE
	adoptEnvelope := CommandEnvelope{
		ProjectID:       b.ProjectID,
		SessionID:       sourceSession,
		TargetID:        checkpoint.ID,
		ExpectedVersion: checkpoint.Generation,
		IdempotencyKey:  "adopt-while-live",
	}
	_, err = service.Adopt(ctx, adoptEnvelope, checkpoint.Digest, destinationSession)
	if err == nil || !errors.Is(err, model.ErrConflict) {
		t.Fatalf("expected adoption to fail while source process is live, got err: %v", err)
	}

	// 3. Stop source session process (simulate crash / exit)
	ClearSessionLiveness(r.ProjectRoot(), sourceSession)
	// The source's old PID is positively absent; a missing marker alone would
	// leave process state unknown and must not permit adoption.
	pidDir := statepath.Path(r.ProjectRoot(), "recovery")
	if err = os.WriteFile(filepath.Join(pidDir, sourceSession+".pid"), []byte("999999999\nsource-birth-stamp\n"), 0600); err != nil {
		t.Fatal(err)
	}

	// Now adoption MUST succeed
	adoptEnvelope.IdempotencyKey = "adopt-after-stopped"
	adopted, err := service.Adopt(ctx, adoptEnvelope, checkpoint.Digest, destinationSession)
	if err != nil {
		t.Fatalf("expected adoption to succeed after source stopped, got: %v", err)
	}
	if adopted.State != "adopted" || adopted.AdoptedSessionID != destinationSession {
		t.Fatalf("unexpected adopted state: %+v", adopted)
	}
}

func TestProcessLivenessVerification_Regression(t *testing.T) {
	tmp := t.TempDir()
	const session = "SESS-liveness"

	// Unknown process state must not permit recovery adoption.
	if VerifySourceProcessStopped("", session, "SESS-other") {
		t.Fatal("empty project root must not prove the source stopped")
	}
	if VerifySourceProcessStopped(tmp, session, "SESS-other") {
		t.Fatal("missing liveness marker must not prove the source stopped")
	}

	// 2. Self-session is never stopped while running
	if VerifySourceProcessStopped(tmp, session, session) {
		t.Fatal("self-session should not be considered stopped")
	}

	// 3. Recorded session with current pid is live
	RecordSessionLiveness(tmp, session)
	if VerifySourceProcessStopped(tmp, session, "SESS-other") {
		t.Fatal("expected active session with live pid to not be stopped")
	}

	// 4. Clearing the marker leaves process state unknown.
	ClearSessionLiveness(tmp, session)
	if VerifySourceProcessStopped(tmp, session, "SESS-other") {
		t.Fatal("cleared liveness marker must not prove the source stopped")
	}

	// 5. A recorded PID that is absent positively proves the source stopped.
	pidDir := statepath.Path(tmp, "recovery")
	_ = os.MkdirAll(pidDir, 0700)
	// PID 999999 is dead
	_ = os.WriteFile(filepath.Join(pidDir, session+".pid"), []byte("999999\n"), 0600)
	if !VerifySourceProcessStopped(tmp, session, "SESS-other") {
		t.Fatal("expected dead pid session to be stopped")
	}

	// A malformed marker and a marker with a live PID but no birth stamp are unknown.
	marker := filepath.Join(pidDir, session+".pid")
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(marker, 0700); err != nil {
		t.Fatal(err)
	}
	if VerifySourceProcessStopped(tmp, session, "SESS-other") {
		t.Fatal("unreadable liveness marker must not prove the source stopped")
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(pidDir, session+".pid"), []byte("corrupt\n"), 0600)
	if VerifySourceProcessStopped(tmp, session, "SESS-other") {
		t.Fatal("corrupt liveness marker must not prove the source stopped")
	}
	_ = os.WriteFile(filepath.Join(pidDir, session+".pid"), []byte(fmt.Sprintf("%d\n", os.Getpid())), 0600)
	if VerifySourceProcessStopped(tmp, session, "SESS-other") {
		t.Fatal("live PID without a birth stamp must remain unknown")
	}

	// A different birth stamp proves that the recorded PID has been reused.
	_ = os.WriteFile(filepath.Join(pidDir, session+".pid"), []byte(fmt.Sprintf("%d\nnot-the-current-birth-stamp\n", os.Getpid())), 0600)
	if !VerifySourceProcessStopped(tmp, session, "SESS-other") {
		t.Fatal("PID reuse with a different birth stamp must prove the source stopped")
	}
}

func TestSessionLivenessRejectsUnsafeSessionIDs_Regression(t *testing.T) {
	tmp := t.TempDir()
	recoveryDir := statepath.Path(tmp, "recovery")
	if err := os.MkdirAll(recoveryDir, 0700); err != nil {
		t.Fatal(err)
	}
	outside := statepath.Path(tmp, "victim.pid")
	if err := os.WriteFile(outside, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"../victim", ".hidden", "a/b", "bad\\name"} {
		RecordSessionLiveness(tmp, id)
		ClearSessionLiveness(tmp, id)
		if VerifySourceProcessStopped(tmp, id, "other") {
			t.Errorf("unsafe session ID %q must not prove source stopped", id)
		}
	}
	entries, err := os.ReadDir(recoveryDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("unsafe IDs created recovery markers: %v, %v", entries, err)
	}
	if data, err := os.ReadFile(outside); err != nil || string(data) != "preserve" {
		t.Fatalf("unsafe ID changed an outside marker: %q, %v", data, err)
	}
}
