package store

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// Checkpoint times are stored as RFC 3339 text with trailing fractional zeros
// trimmed, so a checkpoint on a whole second ("…:07Z") sorts after one half a
// second later ("…:07.5Z") as text. The history and the latest checkpoint must
// follow time, not text.
func TestHandoffCheckpointsFollowTimeNotText(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	first := time.Date(2026, 9, 18, 7, 10, 7, 0, time.UTC)
	for _, cp := range []struct {
		id      string
		harness string
		at      time.Time
	}{
		// Inserted out of time order, so insertion order cannot mask the bug.
		{"CKPT-LATER", "antigravity", first.Add(500 * time.Millisecond)},
		{"CKPT-EARLIER", "codex-cli", first},
	} {
		checkpoint := model.HandoffCheckpoint{
			ID: cp.id, GoalID: "GOAL-1", GoalRevision: 1,
			ConstraintsDigest: "sha256:constraint-1",
			TaskID:            "TASK-ORDER", SessionID: "SESS-1",
			Author:    model.AuthorProvenance{AgentID: cp.harness + "-agent", Harness: cp.harness},
			Role:      "developer",
			CreatedAt: cp.at,
		}
		if err := st.SaveHandoffCheckpoint(ctx, checkpoint); err != nil {
			t.Fatalf("save %s: %v", cp.id, err)
		}
	}

	history, err := st.ListHandoffCheckpoints(ctx, "TASK-ORDER")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[0].ID != "CKPT-EARLIER" || history[1].ID != "CKPT-LATER" {
		var ids []string
		for _, cp := range history {
			ids = append(ids, cp.ID)
		}
		t.Fatalf("history is not in time order: %v", ids)
	}

	latest, err := st.GetLatestHandoffCheckpoint(ctx, "TASK-ORDER")
	if err != nil {
		t.Fatal(err)
	}
	if latest.ID != "CKPT-LATER" {
		t.Fatalf("latest checkpoint is %s, want CKPT-LATER", latest.ID)
	}
}
