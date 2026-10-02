//go:build linux

package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/execution"
)

// The real binary captures a snapshot, previews a rollback without touching
// files, refuses a wrong confirmation, and restores on the right one.
func TestPTYCheckpointCreateAndRollback(t *testing.T) {
	bin := buildMarshalBinary(t)
	sweepWorkEnvironment(t)
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	project := initProject(t, bin)
	t.Chdir(project)
	notes := filepath.Join(project, "notes.txt")
	if err := os.WriteFile(notes, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	terminal := startFrozenTUIInProject(t, 40, 200, bin, project, "tui", "SESSION-pty-rollback")
	terminal.mustSee("MARSHAL")
	terminal.sendLine("/checkpoint create before editing notes")
	terminal.mustSee("captured (digest")

	engine, err := execution.NewCheckpointEngine(project)
	if err != nil {
		t.Fatal(err)
	}
	var records []execution.CheckpointRecord
	for deadline := time.Now().Add(5 * time.Second); len(records) == 0 && time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		records, _ = engine.ListCheckpoints()
	}
	if len(records) != 1 {
		t.Fatalf("checkpoints on disk: %d", len(records))
	}
	cp := records[0]
	if err := os.WriteFile(notes, []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}

	terminal.sendLine("/rollback " + cp.CheckpointID)
	terminal.mustSee("overwrite notes.txt")
	terminal.sendLine("/rollback " + cp.CheckpointID + " confirm 000000000000")
	terminal.mustSee("does not match the snapshot's digest")
	if data, _ := os.ReadFile(notes); string(data) != "edited" {
		t.Fatal("a preview or refused confirmation changed files")
	}
	terminal.sendLine("/rollback " + cp.CheckpointID + " confirm " + cp.SnapshotDigest[:12])
	terminal.mustSee("Rolled back to " + cp.CheckpointID)
	if data, _ := os.ReadFile(notes); string(data) != "original" {
		t.Fatalf("notes.txt after rollback: %q", data)
	}
}
