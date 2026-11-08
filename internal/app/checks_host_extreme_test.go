//go:build !governed

package app

import (
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Marshal build runs required checks on the host by design: no bubblewrap
// or egress proxy can fail them, the command reaches host files and keeps the
// host's capabilities, and the recorded output says it ran unconfined.
func TestExtremeChecksRunOnHost(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := Bootstrap(t.Context(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	r, err := Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	marker := filepath.Join(t.TempDir(), "marker")
	record := r.runGovernedCheck(t.Context(), "RUN-parent", "task", "codex", repo.Path(), "printf ran > '"+marker+"'; printf checked")
	if record.ExitCode != 0 || !strings.HasPrefix(record.Output, "checked") || !strings.Contains(record.Output, "ran unconfined on the host") || !strings.Contains(record.Output, "by design") {
		t.Fatalf("governed check did not run on the host: %+v", record)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "ran" {
		t.Fatalf("check did not reach the host: %q %v", data, err)
	}
	failing := r.runGovernedCheck(t.Context(), "RUN-parent", "task", "codex", repo.Path(), "exit 3")
	if failing.ExitCode != 3 {
		t.Fatalf("a failing check must keep its exit status: %+v", failing)
	}
	clean := testgit.New(t)
	head, err := gitMarshal(t.Context(), clean.Path(), "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	integration := filepath.Join(t.TempDir(), "integration")
	if err := runIntegrationCheck(t.Context(), clean.Path(), head, "printf ran > '"+integration+"'"); err != nil {
		t.Fatalf("integration check refused on the host: %v", err)
	}
	if _, err := os.Stat(integration); err != nil {
		t.Fatalf("integration check did not run on the host: %v", err)
	}
}
