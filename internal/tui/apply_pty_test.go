//go:build linux

package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/execution"
)

// The real binary snapshots the project before a native Codex apply, reports
// the files that actually changed, and the snapshot undoes the apply.
func TestPTYApplyReportsChangedFilesAndCanBeUndone(t *testing.T) {
	bin := buildMarshalBinary(t)
	sweepWorkEnvironment(t)
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	doubles := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1\" = apply ] && [ \"$2\" = cloud-123 ]; then printf 'patched\\n' > applied.txt; echo applied; exit 0; fi\n" +
		"if [ \"$1\" = --version ]; then echo 'codex-cli 0.159.2'; exit 0; fi\nexit 0\n"
	if err := os.WriteFile(filepath.Join(doubles, "codex"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", doubles+string(os.PathListSeparator)+os.Getenv("PATH"))
	project := initProject(t, bin)
	t.Chdir(project)

	terminal := startFrozenTUIInProject(t, 40, 200, bin, project, "tui", "SESSION-pty-apply")
	terminal.mustSee("MARSHAL")
	terminal.sendLine("/apply TASK-CODEX-1")
	terminal.mustSee("is a MARSHAL task ID, not a Codex task ID")
	terminal.sendLine("/apply cloud-123")
	terminal.mustSee("1 file(s) changed: applied.txt")
	if _, err := os.Stat(filepath.Join(project, "applied.txt")); err != nil {
		t.Fatal("apply did not change the project")
	}

	engine, err := execution.NewCheckpointEngine(project)
	if err != nil {
		t.Fatal(err)
	}
	records, err := engine.ListCheckpoints()
	if err != nil || len(records) != 1 || !strings.Contains(records[0].Reason, "before /apply cloud-123") {
		t.Fatalf("snapshot before apply: %+v %v", records, err)
	}
	terminal.sendLine("/rollback " + records[0].CheckpointID + " confirm " + records[0].SnapshotDigest[:12])
	terminal.mustSee("Rolled back to " + records[0].CheckpointID)
	if _, err := os.Stat(filepath.Join(project, "applied.txt")); !os.IsNotExist(err) {
		t.Fatal("rollback did not undo the apply")
	}
}
