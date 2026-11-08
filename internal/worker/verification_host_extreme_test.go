//go:build !governed

package worker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The Marshal build verifies on the host by design: a check reaches host
// files, receives its build directory, and its evidence says it ran
// unconfined, so it is accepted without bubblewrap.
func TestExtremeVerificationRunsOnHost(t *testing.T) {
	dir, output := t.TempDir(), t.TempDir()
	outside := filepath.Join(t.TempDir(), "host-marker")
	script := "printf host > '" + outside + "'; printf built > \"$MARSHAL_BUILD_DIR/out\"; printf ok"
	result, err := RunVerification(t.Context(), dir, []string{"/bin/sh", "-c", script}, 10*time.Second, 4096, output)
	if err != nil || result.ExitCode != 0 || string(result.Stdout) != "ok" {
		t.Fatalf("host verification: %+v %v", result, err)
	}
	if data, err := os.ReadFile(outside); err != nil || string(data) != "host" {
		t.Fatalf("verification did not run on the host: %q %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(output, "out")); err != nil || string(data) != "built" {
		t.Fatalf("build directory not provided: %q %v", data, err)
	}
	if !strings.HasPrefix(result.Isolation.Reason, UnconfinedReasonPrefix) || !strings.Contains(result.Isolation.Reason, "by design") {
		t.Fatalf("evidence does not say it ran on the host: %q", result.Isolation.Reason)
	}
	if err := VerificationResultError(result); err != nil {
		t.Fatalf("host evidence refused: %v", err)
	}
}
