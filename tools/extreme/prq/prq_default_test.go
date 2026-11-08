//go:build governed

package main

import (
	"os/exec"
	"strings"
	"testing"
)

func TestDefaultBuildRefusesExecution(t *testing.T) {
	exe := t.TempDir() + "/prq"
	cmd := exec.Command("go", "build", "-o", exe, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build default prq: %v\n%s", err, out)
	}
	runCmd := exec.Command(exe)
	out, err := runCmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected default prq to exit non-zero")
	}
	if !strings.Contains(string(out), "requires the default Marshal build") {
		t.Fatalf("unexpected output: %s", out)
	}
}
