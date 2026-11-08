package tui

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
)

type mockRestoreAuthority struct {
	fakeAuthority
	snapshots []model.MemoryRestoreSnapshot
	reviews   map[string]app.MemoryRestoreReview
	restored  bool
	created   bool
}

func (m *mockRestoreAuthority) ReviewMemoryActionRestore(_ context.Context, id string) (app.MemoryRestoreReview, error) {
	r, ok := m.reviews[id]
	if !ok {
		return app.MemoryRestoreReview{}, model.ErrNotFound
	}
	return r, nil
}

func (m *mockRestoreAuthority) RestoreMemoryAction(_ context.Context, _ app.CommandEnvelope, req app.MemoryRestoreRequest) (model.MemoryRestoreResult, error) {
	m.restored = true
	return model.MemoryRestoreResult{
		SnapshotID:       req.SnapshotID,
		Generation:       2,
		StateDigest:      "new-state-digest",
		ProjectionStatus: "REBUILT",
		Restored:         []model.MemorySnapshotRecord{{ID: "rec-1", Revision: 1, Digest: "dig-1"}},
	}, nil
}

func (m *mockRestoreAuthority) CreateMemorySnapshotAction(_ context.Context, e app.CommandEnvelope, name string, _ []string) (model.MemoryRestoreSnapshot, error) {
	m.created = true
	snap := model.MemoryRestoreSnapshot{
		ID:        e.TargetID,
		ProjectID: e.ProjectID,
		Name:      name,
		Digest:    "snap-digest",
		CreatedAt: time.Now().UTC(),
		Records:   []model.MemorySnapshotRecord{{ID: "rec-1", Revision: 1, Digest: "dig-1"}},
	}
	m.snapshots = append(m.snapshots, snap)
	if m.reviews == nil {
		m.reviews = make(map[string]app.MemoryRestoreReview)
	}
	m.reviews[snap.ID] = app.MemoryRestoreReview{
		Snapshot:     snap,
		Generation:   1,
		StateDigest:  "state-dig",
		PolicyDigest: "none",
	}
	return snap, nil
}

func (m *mockRestoreAuthority) ListMemoryRestoreSnapshots(_ context.Context) ([]model.MemoryRestoreSnapshot, error) {
	return m.snapshots, nil
}

func TestMemoryRollbackSnapshotCreationAndSelection_Regression(t *testing.T) {
	auth := &mockRestoreAuthority{
		reviews: make(map[string]app.MemoryRestoreReview),
	}
	source := &ControlSource{
		Authority: auth,
		ProjectID: "PROJ-test",
		SessionID: "SESS-test",
	}

	bindings := memoryActionBindings(source)
	b, ok := bindings["CTUI-0071"]
	if !ok {
		t.Fatal("missing CTUI-0071 binding")
	}
	if !b.Bound() {
		t.Fatalf("CTUI-0071 should be bound with restore authority, got requires: %s", b.Requires)
	}

	// 1. Snapshot creation flow when no snapshot exists yet and snapshot_id is empty
	ctx := context.Background()
	req := ActionRequest{
		Action:         "CTUI-0071",
		ProjectID:      source.ProjectID,
		SessionID:      source.SessionID,
		IdempotencyKey: "test-rollback-create",
		Inputs: map[string]string{
			"snapshot_id": "",
			"reason":      "initial restore",
		},
	}
	target, err := b.PrepareRequest(ctx, req)
	if err != nil {
		t.Fatalf("PrepareRequest with empty snapshot_id should trigger snapshot creation/selection: %v", err)
	}
	if !auth.created {
		t.Fatal("snapshot creation flow was not invoked when no snapshots existed")
	}
	if target.ID == "" {
		t.Fatal("prepared target has empty ID")
	}

	// 2. Execute rollback to the selected snapshot
	req.Target = target
	outcome, err := b.Execute(ctx, req)
	if err != nil || !outcome.Succeeded() {
		t.Fatalf("Execute failed: %v, outcome: %+v", err, outcome)
	}
	if !auth.restored {
		t.Fatal("restore action was not executed")
	}

	// 3. Selection flow: when snapshots exist and snapshot_id is empty, select latest
	newerSnap := model.MemoryRestoreSnapshot{
		ID:        "SNAP-newer",
		ProjectID: source.ProjectID,
		Name:      "newer",
		Digest:    "digest-newer",
		CreatedAt: time.Now().UTC().Add(time.Hour),
	}
	auth.snapshots = append(auth.snapshots, newerSnap)
	auth.reviews[newerSnap.ID] = app.MemoryRestoreReview{
		Snapshot:     newerSnap,
		Generation:   2,
		StateDigest:  "state-2",
		PolicyDigest: "none",
	}

	req2 := ActionRequest{
		Action:         "CTUI-0071",
		ProjectID:      source.ProjectID,
		SessionID:      source.SessionID,
		IdempotencyKey: "test-rollback-select",
		Inputs: map[string]string{
			"snapshot_id": "",
			"reason":      "restore latest",
		},
	}
	target2, err := b.PrepareRequest(ctx, req2)
	if err != nil {
		t.Fatalf("PrepareRequest failed on snapshot selection: %v", err)
	}
	if target2.ID != "SNAP-newer" {
		t.Fatalf("expected latest snapshot SNAP-newer to be selected, got %s", target2.ID)
	}
}
