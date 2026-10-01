//go:build linux

package tui

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
)

// The real binary inspects and compares two snapshots, and reports a
// snapshot whose files were changed after capture instead of comparing it.
func TestPTYCheckpointInspectAndDiff(t *testing.T) {
	bin := buildMarshalBinary(t)
	sweepWorkEnvironment(t)
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	project := initProject(t, bin)
	t.Chdir(project)
	ctx := context.Background()
	runtime, err := app.Open(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	service := runtime.Execution()
	first, err := service.CreateCheckpoint(ctx, "run-pty", "task-pty", "before edit")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "added.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := service.CreateCheckpoint(ctx, "run-pty", "task-pty", "after edit")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}

	terminal := startFrozenTUIInProject(t, 40, 200, bin, project, "tui", "SESSION-pty-checkpoint")
	terminal.mustSee("MARSHAL")
	terminal.sendLine("/checkpoint inspect " + first.CheckpointID)
	terminal.mustSee("Files:       INTACT")
	terminal.sendLine("/checkpoint diff " + first.CheckpointID + " " + second.CheckpointID)
	terminal.mustSee("+ added.txt")

	if err := os.WriteFile(filepath.Join(second.WorktreePath, "added.txt"), []byte("forged"), 0o644); err != nil {
		t.Fatal(err)
	}
	terminal.sendLine("/checkpoint inspect " + second.CheckpointID)
	terminal.mustSee("TAMPERED")
}
