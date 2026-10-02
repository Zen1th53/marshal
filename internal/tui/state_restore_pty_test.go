//go:build linux

package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/store"
)

// The real binary backs up its state, changes it, previews and then restores
// the backup from the live session, and works on the restored state.
func TestPTYBackupRestoreFromLiveSession(t *testing.T) {
	bin := buildMarshalBinary(t)
	sweepWorkEnvironment(t)
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	project := initProject(t, bin)
	t.Chdir(project)
	terminal := startFrozenTUIInProject(t, 40, 220, bin, project, "tui", "SESSION-pty-restore")
	terminal.mustSee("MARSHAL")
	terminal.sendLine("/backup create")
	terminal.mustSee("Backup written and verified")
	var backup string
	for deadline := time.Now().Add(5 * time.Second); backup == "" && time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		matches, _ := filepath.Glob(filepath.Join(project, ".marshal", "backups", "backup-*.db"))
		if len(matches) == 1 {
			backup = matches[0]
		}
	}
	if backup == "" {
		t.Fatal("backup file not found")
	}
	meta, err := store.VerifyBackup(t.Context(), backup, "", 0)
	if err != nil {
		t.Fatal(err)
	}

	terminal.sendLine("/task create work done after the backup")
	terminal.mustSee("work done after the backup")
	terminal.sendLine("/backup restore " + backup)
	terminal.mustSee("Nothing has changed yet")
	terminal.sendLine("/backup restore " + backup + " confirm " + strings.TrimPrefix(meta.DatabaseSHA256, "sha256:")[:12])
	terminal.mustSee("Project state restored")
	terminal.sendLine("/tasks")
	terminal.mustSee("No tasks in store")

	recoveries, _ := filepath.Glob(filepath.Join(project, ".marshal", "backups", "pre-restore-*.db"))
	if len(recoveries) != 1 {
		t.Fatalf("recovery backups: %v", recoveries)
	}
	if _, err := os.Stat(recoveries[0]); err != nil {
		t.Fatal(err)
	}
}
